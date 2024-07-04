package redistest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
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
	dockertest.DockerItemGenerator
	ImageName     string
	ContainerPort nat.Port
	containerName string
	waitInterval  time.Duration // 等待容器启动的间隔
	maxWaitTime   time.Duration // 最大等待容器启动的时间
}

func New(generator dockertest.DockerItemGenerator, opts ...Option) *Redis {
	res := &Redis{
		DockerItemGenerator: generator,
		ImageName:           "redis:6",
		ContainerPort:       "6379/tcp",
		containerName:       "redis_test",
		waitInterval:        200 * time.Millisecond,
		maxWaitTime:         10 * time.Second,
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

func WithMaxWaitTime(interval time.Duration) Option {
	return optionFunc(func(r *Redis) {
		r.maxWaitTime = interval
	})
}

// RunInDocker runs the tests with
// a redis instance in a docker container.
func (r *Redis) RunInDocker(m *testing.M) int {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("panic", "err", err)
		}
	}()

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
		}, nil, nil, r.containerName)
	if err != nil {
		panic(err)
	}
	containerID := resp.ID

	// ContainerStart 根据containerID,向 docker 守护进程发送请求以启动容器。
	err = cli.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		panic(err)
	}

	var insRes types.ContainerJSON

	handCtx, cancel := context.WithTimeout(context.Background(), r.maxWaitTime)
	ticker := time.NewTicker(r.waitInterval)
	var done bool
	for {
		select {
		case <-ticker.C:
			// ContainerInspect 返回容器信息。
			insRes, err = cli.ContainerInspect(ctx, resp.ID)
			if err != nil {
				panic(err)
			}
			switch insRes.State.Status {
			case "created", "restarting":
				continue
			case "running", "paused", "removing", "exited", "dead":
				done = true
			}
		case <-handCtx.Done():
			done = true
		}
		if done {
			break
		}
	}
	cancel()
	ticker.Stop()

	defer func() {
		// ContainerRemove 根据containerID,杀死并从 docker 主机中删除一个容器。
		err = cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
		if err != nil {
			panic(err)
		}

		// 循环删除容器里所有的挂载
		if insRes.Mounts != nil {
			for _, res := range insRes.Mounts {
				err := cli.VolumeRemove(ctx, res.Name, true)
				if err != nil {
					panic(err)
				}
			}
		}
	}()

	if insRes.State != nil && insRes.State.ExitCode != 0 {
		slog.Error("容器启动异常", "ExitCode", insRes.State.ExitCode, "Status", insRes.State.Status)
	}

	// Ports是PortBinding的集合
	hostPort := insRes.NetworkSettings.Ports[r.ContainerPort][0]
	redisAddr = fmt.Sprintf("%s:%s", hostIP, hostPort.HostPort)
	slog.Info("redis addr", "addr", redisAddr)

	return m.Run()
}

// NewClient creates a redis client connected to the redis instance in docker.
func NewClient(ctx context.Context, opt ...redis.Options) (redis.Cmdable, error) {
	// 这里做一个保护
	if redisAddr == "" {
		return nil, fmt.Errorf("redis addr not set. Please run Redis.RunInDocker in TestMain")
	}
	var o redis.Options
	switch len(opt) {
	case 0:
	case 1:
		o = opt[0]
	default:
		return nil, fmt.Errorf("redis options struct can only pass one")
	}
	o.Addr = redisAddr
	// 全局模式
	rdb := redis.NewClient(&o)
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	return rdb, nil
}
