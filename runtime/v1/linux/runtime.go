// +build linux

/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package linux

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	eventstypes "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/events/exchange"
	"github.com/containerd/containerd/identifiers"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/process"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/runtime"
	"github.com/containerd/containerd/runtime/linux/runctypes"
	v1 "github.com/containerd/containerd/runtime/v1"
	shim "github.com/containerd/containerd/runtime/v1/shim/v1"
	runc "github.com/containerd/go-runc"
	"github.com/containerd/typeurl"
	ptypes "github.com/gogo/protobuf/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	pluginID = fmt.Sprintf("%s.%s", plugin.RuntimePlugin, "linux")
	empty    = &ptypes.Empty{}
)

const (
	configFilename = "config.json"
	defaultRuntime = "runc"
	defaultShim    = "containerd-shim"
)

func init() {
	plugin.Register(&plugin.Registration{
		// Type 为 "io.containerd.runtime.v1"
		Type: plugin.RuntimePlugin,
		// ID 为 "linux"
		ID:     "linux",
		InitFn: New,
		Requires: []plugin.Type{
			plugin.MetadataPlugin,
		},
		// defaultShim为"containerd-shim", defaultRuntime为"runc"
		Config: &Config{
			Shim:    defaultShim,
			Runtime: defaultRuntime,
		},
	})
}

var _ = (runtime.PlatformRuntime)(&Runtime{})

// Config options for the runtime
type Config struct {
	// Shim is a path or name of binary implementing the Shim GRPC API
	Shim string `toml:"shim"`
	// Runtime is a path or name of an OCI runtime used by the shim
	Runtime string `toml:"runtime"`
	// RuntimeRoot is the path that shall be used by the OCI runtime for its data
	RuntimeRoot string `toml:"runtime_root"`
	// NoShim calls runc directly from within the pkg
	NoShim bool `toml:"no_shim"`
	// Debug enable debug on the shim
	ShimDebug bool `toml:"shim_debug"`
}

// New returns a configured runtime
func New(ic *plugin.InitContext) (interface{}, error) {
	ic.Meta.Platforms = []ocispec.Platform{platforms.DefaultSpec()}

	// 创建 root state 目录
	if err := os.MkdirAll(ic.Root, 0711); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(ic.State, 0711); err != nil {
		return nil, err
	}
	// 得到 containers.Store 对象
	m, err := ic.Get(plugin.MetadataPlugin)
	if err != nil {
		return nil, err
	}
	// 读取配置信息, 创建Runtime对象
	cfg := ic.Config.(*Config)
	r := &Runtime{
		root:       ic.Root,
		state:      ic.State,
		tasks:      runtime.NewTaskList(),
		containers: metadata.NewContainerStore(m.(*metadata.DB)),
		address:    ic.Address,
		events:     ic.Events,
		config:     cfg,
	}
	// restore 所有tasks, 记录到<tasks>中
	tasks, err := r.restoreTasks(ic.Context)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if err := r.tasks.AddWithNamespace(t.namespace, t); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Runtime for a linux based system
type Runtime struct {
	root    string
	state   string
	address string

	tasks      *runtime.TaskList
	containers containers.Store
	events     *exchange.Exchange

	config *Config
}

// ID of the runtime
func (r *Runtime) ID() string {
	return pluginID
}

// Create a new task
func (r *Runtime) Create(ctx context.Context, id string,
	opts runtime.CreateOpts) (_ runtime.Task, err error) {
	// 读取对应namespace
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	// 确认id合法
	if err := identifiers.Validate(id); err != nil {
		return nil, errors.Wrapf(err, "invalid task id")
	}

	// 从<containers>读取container信息, 构建RuncOptions
	ropts, err := r.getRuncOptions(ctx, id)
	if err != nil {
		return nil, err
	}

	// 创建bundle对象
	// 包括:
	//	runc需要的启动目录:
	// 		path: runc启动的目录 "/run/containerd/io.containerd.runtime.v1.linux/[ns]/[cid]"
	// 		workdir: "/run/docker/runtime-runc/[ns]/[cid]"
	bundle, err := newBundle(id,
		filepath.Join(r.state, namespace),
		filepath.Join(r.root, namespace),
		opts.Spec.Value)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			bundle.Delete()
		}
	}()

	// 本地shim进程配置
	shimopt := ShimLocal(r.config, r.events)
	// remote shim配置
	if !r.config.NoShim {
		var cgroup string
		if opts.TaskOptions != nil {
			v, err := typeurl.UnmarshalAny(opts.TaskOptions)
			if err != nil {
				return nil, err
			}
			cgroup = v.(*runctypes.CreateOptions).ShimCgroup
		}
		exitHandler := func() {
			log.G(ctx).WithField("id", id).Info("shim reaped")

			if _, err := r.tasks.Get(ctx, id); err != nil {
				// Task was never started or was already successfully deleted
				return
			}

			if err = r.cleanupAfterDeadShim(context.Background(), bundle, namespace, id); err != nil {
				log.G(ctx).WithError(err).WithFields(logrus.Fields{
					"id":        id,
					"namespace": namespace,
				}).Warn("failed to clean up after killed shim")
			}
		}
		shimopt = ShimRemote(r.config, r.address, cgroup, exitHandler)
	}

	// 创建shim client
	s, err := bundle.NewShimClient(ctx, namespace, shimopt, ropts)
	if err != nil {
		return nil, err
	}
	// 回滚操作, kill shim进程
	defer func() {
		if err != nil {
			if kerr := s.KillShim(ctx); kerr != nil {
				log.G(ctx).WithError(err).Error("failed to kill shim")
			}
		}
	}()

	// 构建创建Task req
	rt := r.config.Runtime
	if ropts != nil && ropts.Runtime != "" {
		rt = ropts.Runtime
	}
	sopts := &shim.CreateTaskRequest{
		ID:         id,
		Bundle:     bundle.path,
		Runtime:    rt,
		Stdin:      opts.IO.Stdin,
		Stdout:     opts.IO.Stdout,
		Stderr:     opts.IO.Stderr,
		Terminal:   opts.IO.Terminal,
		Checkpoint: opts.Checkpoint,
		Options:    opts.TaskOptions,
	}
	for _, m := range opts.Rootfs {
		sopts.Rootfs = append(sopts.Rootfs, &types.Mount{
			Type:    m.Type,
			Source:  m.Source,
			Options: m.Options,
		})
	}

	// 调用ShimClient.Create()创建shim
	cr, err := s.Create(ctx, sopts)
	if err != nil {
		return nil, errdefs.FromGRPC(err)
	}

	// 构建Task结构
	t, err := newTask(id, namespace, int(cr.Pid), s, r.events, r.tasks, bundle)
	if err != nil {
		return nil, err
	}

	// 记录到<tasks>
	if err := r.tasks.Add(ctx, t); err != nil {
		return nil, err
	}
	// 推送create event
	r.events.Publish(ctx, runtime.TaskCreateEventTopic, &eventstypes.TaskCreate{
		ContainerID: sopts.ID,
		Bundle:      sopts.Bundle,
		Rootfs:      sopts.Rootfs,
		IO: &eventstypes.TaskIO{
			Stdin:    sopts.Stdin,
			Stdout:   sopts.Stdout,
			Stderr:   sopts.Stderr,
			Terminal: sopts.Terminal,
		},
		Checkpoint: sopts.Checkpoint,
		Pid:        uint32(t.pid),
	})

	return t, nil
}

// Tasks returns all tasks known to the runtime
func (r *Runtime) Tasks(ctx context.Context, all bool) ([]runtime.Task, error) {
	return r.tasks.GetAll(ctx, all)
}

func (r *Runtime) restoreTasks(ctx context.Context) ([]*Task, error) {
	// 读取state目录下所有子目录(每个目录代表一个namespace)
	dir, err := ioutil.ReadDir(r.state)
	if err != nil {
		return nil, err
	}
	var o []*Task
	// 遍历所有namespace
	for _, namespace := range dir {
		if !namespace.IsDir() {
			continue
		}
		// 读取并检查namespace名字
		name := namespace.Name()
		// skip hidden directories
		if len(name) > 0 && name[0] == '.' {
			continue
		}
		log.G(ctx).WithField("namespace", name).Debug("loading tasks in namespace")
		// 加载并记录namespace下所有task
		tasks, err := r.loadTasks(ctx, name)
		if err != nil {
			return nil, err
		}
		o = append(o, tasks...)
	}
	return o, nil
}

// Get a specific task by task id
func (r *Runtime) Get(ctx context.Context, id string) (runtime.Task, error) {
	return r.tasks.Get(ctx, id)
}

// Add a runtime task
func (r *Runtime) Add(ctx context.Context, task runtime.Task) error {
	return r.tasks.Add(ctx, task)
}

// Delete a runtime task
func (r *Runtime) Delete(ctx context.Context, id string) {
	r.tasks.Delete(ctx, id)
}

func (r *Runtime) loadTasks(ctx context.Context, ns string) ([]*Task, error) {
	// 读取namespace目录下所有子目录
	dir, err := ioutil.ReadDir(filepath.Join(r.state, ns))
	if err != nil {
		return nil, err
	}
	var o []*Task
	// 遍历子目录, 读取task信息
	for _, path := range dir {
		if !path.IsDir() {
			continue
		}
		// 目录的名字是 taskid, 也就是container id
		id := path.Name()
		// skip hidden directories
		if len(id) > 0 && id[0] == '.' {
			continue
		}
		// 创建bundle对象
		// 两个目录分别是
		// 	1. runc 启动使用的build目录: "/run/containerd/io.containerd.runtime.v1.linux/[ns]/[cid]"
		// 	2. task工作目录: "/run/docker/runtime-runc/[ns]/[cid]"
		bundle := loadBundle(
			id,
			filepath.Join(r.state, ns, id),
			filepath.Join(r.root, ns, id),
		)
		ctx = namespaces.WithNamespace(ctx, ns)
		// 读取 init进程对应pid, task元信息目录的"init.pid"文件
		pid, _ := runc.ReadPidFile(filepath.Join(bundle.path, process.InitPidFile))
		// 创建连接到shim service的client
		shimExit := make(chan struct{})
		s, err := bundle.NewShimClient(ctx, ns, ShimConnect(r.config, func() {
			defer close(shimExit)
			if _, err := r.tasks.Get(ctx, id); err != nil {
				// Task was never started or was already successfully deleted
				return
			}

			if err := r.cleanupAfterDeadShim(ctx, bundle, ns, id); err != nil {
				log.G(ctx).WithError(err).WithField("bundle", bundle.path).
					Error("cleaning up after dead shim")
			}
		}), nil)
		if err != nil {
			log.G(ctx).WithError(err).WithFields(logrus.Fields{
				"id":        id,
				"namespace": ns,
			}).Error("connecting to shim")
			err := r.cleanupAfterDeadShim(ctx, bundle, ns, id)
			if err != nil {
				log.G(ctx).WithError(err).WithField("bundle", bundle.path).
					Error("cleaning up after dead shim")
			}
			continue
		}

		logDirPath := filepath.Join(r.root, ns, id)

		copyAndClose := func(dst io.Writer, src io.ReadWriteCloser) {
			copyDone := make(chan struct{})
			go func() {
				io.Copy(dst, src)
				close(copyDone)
			}()
			select {
			case <-shimExit:
			case <-copyDone:
			}
			src.Close()
		}
		shimStdoutLog, err := v1.OpenShimStdoutLog(ctx, logDirPath)
		if err != nil {
			log.G(ctx).WithError(err).WithFields(logrus.Fields{
				"id":         id,
				"namespace":  ns,
				"logDirPath": logDirPath,
			}).Error("opening shim stdout log pipe")
			continue
		}
		if r.config.ShimDebug {
			go copyAndClose(os.Stdout, shimStdoutLog)
		} else {
			go copyAndClose(ioutil.Discard, shimStdoutLog)
		}

		shimStderrLog, err := v1.OpenShimStderrLog(ctx, logDirPath)
		if err != nil {
			log.G(ctx).WithError(err).WithFields(logrus.Fields{
				"id":         id,
				"namespace":  ns,
				"logDirPath": logDirPath,
			}).Error("opening shim stderr log pipe")
			continue
		}
		if r.config.ShimDebug {
			go copyAndClose(os.Stderr, shimStderrLog)
		} else {
			go copyAndClose(ioutil.Discard, shimStderrLog)
		}

		// 创建对应的Task对象, 并记录
		t, err := newTask(id, ns, pid, s, r.events, r.tasks, bundle)
		if err != nil {
			log.G(ctx).WithError(err).Error("loading task type")
			continue
		}
		o = append(o, t)
	}
	return o, nil
}

func (r *Runtime) cleanupAfterDeadShim(ctx context.Context, bundle *bundle, ns, id string) error {
	log.G(ctx).WithFields(logrus.Fields{
		"id":        id,
		"namespace": ns,
	}).Warn("cleaning up after shim dead")

	pid, _ := runc.ReadPidFile(filepath.Join(bundle.path, process.InitPidFile))
	ctx = namespaces.WithNamespace(ctx, ns)
	if err := r.terminate(ctx, bundle, ns, id); err != nil {
		if r.config.ShimDebug {
			return errors.Wrap(err, "failed to terminate task, leaving bundle for debugging")
		}
		log.G(ctx).WithError(err).Warn("failed to terminate task")
	}

	// Notify Client
	exitedAt := time.Now().UTC()
	r.events.Publish(ctx, runtime.TaskExitEventTopic, &eventstypes.TaskExit{
		ContainerID: id,
		ID:          id,
		Pid:         uint32(pid),
		ExitStatus:  128 + uint32(unix.SIGKILL),
		ExitedAt:    exitedAt,
	})

	r.tasks.Delete(ctx, id)
	if err := bundle.Delete(); err != nil {
		log.G(ctx).WithError(err).Error("delete bundle")
	}
	// kill shim
	if shimPid, err := runc.ReadPidFile(filepath.Join(bundle.path, "shim.pid")); err == nil && shimPid > 0 {
		unix.Kill(shimPid, unix.SIGKILL)
	}

	r.events.Publish(ctx, runtime.TaskDeleteEventTopic, &eventstypes.TaskDelete{
		ContainerID: id,
		Pid:         uint32(pid),
		ExitStatus:  128 + uint32(unix.SIGKILL),
		ExitedAt:    exitedAt,
	})

	return nil
}

func (r *Runtime) terminate(ctx context.Context, bundle *bundle, ns, id string) error {
	rt, err := r.getRuntime(ctx, ns, id)
	if err != nil {
		return err
	}
	if err := rt.Delete(ctx, id, &runc.DeleteOpts{
		Force: true,
	}); err != nil {
		log.G(ctx).WithError(err).Warnf("delete runtime state %s", id)
	}
	if err := mount.Unmount(filepath.Join(bundle.path, "rootfs"), 0); err != nil {
		log.G(ctx).WithError(err).WithFields(logrus.Fields{
			"path": bundle.path,
			"id":   id,
		}).Warnf("unmount task rootfs")
	}
	return nil
}

func (r *Runtime) getRuntime(ctx context.Context, ns, id string) (*runc.Runc, error) {
	ropts, err := r.getRuncOptions(ctx, id)
	if err != nil {
		return nil, err
	}

	var (
		cmd  = r.config.Runtime
		root = process.RuncRoot
	)
	if ropts != nil {
		if ropts.Runtime != "" {
			cmd = ropts.Runtime
		}
		if ropts.RuntimeRoot != "" {
			root = ropts.RuntimeRoot
		}
	}

	return &runc.Runc{
		Command:      cmd,
		LogFormat:    runc.JSON,
		PdeathSignal: unix.SIGKILL,
		Root:         filepath.Join(root, ns),
		Debug:        r.config.ShimDebug,
	}, nil
}

func (r *Runtime) getRuncOptions(ctx context.Context, id string) (*runctypes.RuncOptions, error) {
	container, err := r.containers.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if container.Runtime.Options != nil {
		v, err := typeurl.UnmarshalAny(container.Runtime.Options)
		if err != nil {
			return nil, err
		}
		ropts, ok := v.(*runctypes.RuncOptions)
		if !ok {
			return nil, errors.New("invalid runtime options format")
		}

		return ropts, nil
	}
	return &runctypes.RuncOptions{}, nil
}
