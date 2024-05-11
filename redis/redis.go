package redis

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
	"github.com/redis/go-redis/v9"

	"github.com/udugong/testing-with-docker"
)

var redisAddr string

type Redis struct {
	dockertesting.DockerItemGenerator
	ImageName     string
	ContainerPort nat.Port
	waitInterval  time.Duration // 等待容器启动的间隔
}

func New(generator dockertesting.DockerItemGenerator, opts ...Option) *Redis {
	res := &Redis{
		DockerItemGenerator: generator,
		ImageName:           "redis:6",
		ContainerPort:       "6379/tcp",
		waitInterval:        time.Second,
	}
	for _, opt := range opts {
		opt.apply(res)
	}
	return res
}

type Option interface {
	apply(*Redis)
}

type optionFunc func(*Redis)

func (f optionFunc) apply(r *Redis) { f(r) }

func WithImageName(name string) Option {
	return optionFunc(func(r *Redis) {
		r.ImageName = name
	})
}

func WithContainerPort(port nat.Port) Option {
	return optionFunc(func(r *Redis) {
		r.ContainerPort = port
	})
}

func WithWaitInterval(interval time.Duration) Option {
	return optionFunc(func(r *Redis) {
		r.waitInterval = interval
	})
}

// RunInDocker runs the tests with
// a redis instance in a docker container.
func (r *Redis) RunInDocker(m *testing.M) int {
	ctx := context.Background()
	cli, hostIP, bindingHostIP := r.Generate()

	// 根据镜像名,查询已存在的镜像
	filter := filters.NewArgs(filters.Arg("reference", r.ImageName))
	images, err := cli.ImageList(context.Background(), image.ListOptions{Filters: filter})
	if err != nil {
		panic(err)
	}

	// 如果没有这个镜像,则去pull
	if len(images) == 0 {
		// ImagePull 请求 docker 主机从远程注册表中提取镜像。
		// 如果操作未经授权，它会执行特权功能并再试一次。由调用者来处理 io.ReadCloser 并正确关闭它。
		out, err := cli.ImagePull(context.Background(), r.ImageName, image.PullOptions{})
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
		Image: r.ImageName, // 对应docker镜像的名字
		ExposedPorts: nat.PortSet{ // 内部要暴露端口列表(比如mysql:3306,redis:6379)
			r.ContainerPort: {},
		},
		Cmd: strslice.StrSlice{
			// "redis-server --protected-mode no",
		},
	},
		// 主机的配置。保存不可移植的配置信息。
		&container.HostConfig{
			// 暴露端口(容器)和主机之间的端口映射
			PortBindings: nat.PortMap{
				r.ContainerPort: []nat.PortBinding{
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

	time.Sleep(r.waitInterval)

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
	hostPort := insRes.NetworkSettings.Ports[r.ContainerPort][0]
	redisAddr = fmt.Sprintf("%s:%s", hostIP, hostPort.HostPort)

	return m.Run()
}

// NewClient creates a redis client connected to the redis instance in docker.
func NewClient(ctx context.Context) (redis.Cmdable, error) {
	// 这里做一个保护
	if redisAddr == "" {
		return nil, fmt.Errorf("redis addr not set. Please run Redis.RunInDocker in TestMain")
	}

	// 全局模式
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	return rdb, nil
}
