package mgotest

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/go-connections/nat"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/udugong/testing-with-docker"
)

var mongoURI string

type MongoDB struct {
	dockertest.DockerItemGenerator
	ImageName     string
	ContainerPort nat.Port
	connOptions   string
	waitInterval  time.Duration // 等待容器启动的间隔
}

func New(dockerItemGenerator dockertest.DockerItemGenerator, opts ...Option) *MongoDB {
	res := &MongoDB{
		DockerItemGenerator: dockerItemGenerator,
		ImageName:           "mongo:7.0",
		ContainerPort:       "27017/tcp",
		connOptions:         "",
		waitInterval:        time.Second,
	}
	for _, opt := range opts {
		opt.apply(res)
	}
	return res
}

type Option interface {
	apply(*MongoDB)
}

type optionFunc func(*MongoDB)

func (f optionFunc) apply(m *MongoDB) { f(m) }

func WithImageName(name string) Option {
	return optionFunc(func(m *MongoDB) {
		m.ImageName = name
	})
}

func WithContainerPort(port nat.Port) Option {
	return optionFunc(func(m *MongoDB) {
		m.ContainerPort = port
	})
}

func WithConnOptions(opts string) Option {
	return optionFunc(func(m *MongoDB) {
		m.connOptions = opts
	})
}

func WithWaitInterval(interval time.Duration) Option {
	return optionFunc(func(m *MongoDB) {
		m.waitInterval = interval
	})
}

// RunInDocker runs the tests with
// a mongodb instance in a docker container.
func (mgo *MongoDB) RunInDocker(m *testing.M) int {
	ctx := context.Background()
	cli, hostIP, bindingHostIP := mgo.Generate()

	// 根据镜像名,查询已存在的镜像
	filter := filters.NewArgs(filters.Arg("reference", mgo.ImageName))
	images, err := cli.ImageList(context.Background(), image.ListOptions{Filters: filter})
	if err != nil {
		panic(err)
	}

	// 如果没有这个镜像,则去pull
	if len(images) == 0 {
		// ImagePull 请求 docker 主机从远程注册表中提取镜像。
		// 如果操作未经授权，它会执行特权功能并再试一次。由调用者来处理 io.ReadCloser 并正确关闭它。
		out, err := cli.ImagePull(context.Background(), mgo.ImageName, image.PullOptions{})
		if err != nil {
			panic(err)
		}
		defer func(out io.ReadCloser) {
			_ = out.Close()
		}(out)
		_, _ = io.Copy(os.Stdout, out)
	}

	// 创建容器
	resp, err := cli.ContainerCreate(ctx, &container.Config{
		// 容器的配置数据。只保存容器的可移植配置信息。
		// “可移植”意味着“独立于我们运行的主机”(容器内部的配置)。
		Image: mgo.ImageName, // 对应docker镜像的名字
		ExposedPorts: nat.PortSet{ // 内部要暴露端口列表(比如mysql:3306,redis:6379)
			mgo.ContainerPort: {},
		},
		Cmd: strslice.StrSlice{},
	},
		// 主机的配置。保存不可移植的配置信息。
		&container.HostConfig{
			// 暴露端口(容器)和主机之间的端口映射
			PortBindings: nat.PortMap{
				mgo.ContainerPort: []nat.PortBinding{
					{
						HostIP:   bindingHostIP, // 主机IP地址
						HostPort: "0",           // 主机端口号。0代表随机端口
					},
				},
			},
		}, nil, nil, "")
	if err != nil {
		panic(err)
	}
	containerID := resp.ID

	// ContainerStart 根据containerID,向 docker 守护进程发送请求以启动容器。
	err = cli.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		panic(err)
	}

	time.Sleep(mgo.waitInterval)

	// ContainerInspect 返回容器信息。
	insRes, err := cli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		panic(err)
	}

	defer func() {
		// ContainerRemove 根据containerID,杀死并从 docker 主机中删除一个容器。
		err = cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
		if err != nil {
			panic(err)
		}

		// 循环删除容器里所有的挂载
		for _, res := range insRes.Mounts {
			err := cli.VolumeRemove(ctx, res.Name, true)
			if err != nil {
				panic(err)
			}
		}
	}()

	if insRes.State.ExitCode != 0 {
		panic("容器启动失败")
	}

	// Ports是PortBinding的集合
	hostPort := insRes.NetworkSettings.Ports[mgo.ContainerPort][0]
	mongoURI = fmt.Sprintf("mongodb://%s:%s/?%s", hostIP, hostPort.HostPort, mgo.connOptions)

	return m.Run()
}

// NewClient creates a client connected to the mongo instance in docker.
func NewClient(ctx context.Context) (*mongo.Client, error) {
	if mongoURI == "" {
		return nil, fmt.Errorf("mongo uri not set. Please run MongoDB.RunInDocker in TestMain")
	}
	conn, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		return nil, err
	}
	err = conn.Ping(ctx, readpref.Primary())
	if err != nil {
		return nil, err
	}
	return conn, nil
}
