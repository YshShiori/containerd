module github.com/containerd/containerd

go 1.13

require (
	github.com/BurntSushi/toml v0.3.1
	github.com/Microsoft/go-winio v0.4.14
	github.com/Microsoft/hcsshim v0.8.7-0.20190820203702-9e921883ac92
	github.com/containerd/aufs v0.0.0-20190114185352-f894a800659b
	github.com/containerd/btrfs v0.0.0-20181101203652-af5082808c83
	github.com/containerd/cgroups v0.0.0-20190717030353-c4b9ac5c7601
	github.com/containerd/console v0.0.0-20181022165439-0650fd9eeb50
	github.com/containerd/continuity v0.0.0-20190815185530-f2a389ac0a02
	github.com/containerd/cri v1.11.1-0.20190926212009-5d49e7e51b43
	github.com/containerd/fifo v0.0.0-20190816180239-bda0ff6ed73c
	github.com/containerd/go-cni v0.0.0-20190813230227-49fbd9b210f3 // indirect
	github.com/containerd/go-runc v0.0.0-20190911050354-e029b79d8cda
	github.com/containerd/ttrpc v0.0.0-20190828172938-92c8520ef9f8
	github.com/containerd/typeurl v0.0.0-20180627222232-a93fcdb778cd
	github.com/containerd/zfs v0.0.0-20190829050200-2ceb2dbb8154
	github.com/containernetworking/cni v0.7.1 // indirect
	github.com/containernetworking/plugins v0.7.6 // indirect
	github.com/docker/distribution v2.7.1-0.20190205005809-0d3efadf0154+incompatible
	github.com/docker/docker v1.4.2-0.20171019062838-86f080cff091 // indirect
	github.com/docker/go-events v0.0.0-20170721190031-9461782956ad
	github.com/docker/go-metrics v0.0.0-20180131145841-4ea375f7759c
	github.com/docker/go-units v0.4.0
	github.com/godbus/dbus v0.0.0-20151105175453-c7fdd8b5cd55 // indirect
	github.com/gogo/googleapis v1.2.0
	github.com/gogo/protobuf v1.2.2-0.20190723190241-65acae22fc9d
	github.com/google/go-cmp v0.3.0
	github.com/google/uuid v1.1.1
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/go-multierror v1.0.0
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.7
	github.com/mistifyio/go-zfs v2.1.2-0.20190413222219-f784269be439+incompatible // indirect
	github.com/opencontainers/go-digest v1.0.0-rc1.0.20180430190053-c9281466c8b2
	github.com/opencontainers/image-spec v1.0.1
	github.com/opencontainers/runc v1.0.0-rc8.0.20190926000215-3e425f80a8c9
	github.com/opencontainers/runtime-spec v1.0.2-0.20190207185410-29686dbc5559
	github.com/opencontainers/selinux v1.2.2 // indirect
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v0.9.2
	github.com/satori/go.uuid v1.2.0 // indirect
	github.com/seccomp/libseccomp-golang v0.9.1 // indirect
	github.com/sirupsen/logrus v1.4.2
	github.com/syndtr/gocapability v0.0.0-20180916011248-d98352740cb2
	github.com/tchap/go-patricia v2.2.6+incompatible // indirect
	github.com/urfave/cli v1.22.0
	go.etcd.io/bbolt v1.3.3
	golang.org/x/net v0.0.0-20190812203447-cdfb69ac37fc
	golang.org/x/sync v0.0.0-20190423024810-112230192c58
	golang.org/x/sys v0.0.0-20190812073006-9eafafc0a87e
	google.golang.org/grpc v1.23.0
	gotest.tools v2.2.0+incompatible
	k8s.io/apiserver v0.0.0-20190913201147-5669a5603d96 // indirect
	k8s.io/cri-api v0.0.0-20190828162817-608eb1dad4ac // indirect
)
