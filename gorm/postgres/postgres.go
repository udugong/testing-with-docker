package pgtest

import (
	"context"
	"database/sql"
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
	"github.com/docker/go-connections/nat"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/udugong/testing-with-docker"
)

var postgresDSN string

type Postgres struct {
	dockertest.DockerItemGenerator
	ImageName     string
	ContainerPort nat.Port
	DatabaseName  string
	connOptions   string
	containerEnv  []string
	containerCmd  []string
	containerName string
	waitInterval  time.Duration // 等待容器启动的间隔
	maxWaitTime   time.Duration // 最大等待容器启动的时间
}

func New(generator dockertest.DockerItemGenerator, databaseName string, opts ...Option) *Postgres {
	res := defaultPostgres(generator, databaseName)
	for _, opt := range opts {
		opt.apply(res)
	}
	return res
}

func defaultPostgres(generator dockertest.DockerItemGenerator, databaseName string) *Postgres {
	return &Postgres{
		DockerItemGenerator: generator,
		ImageName:           "postgres:16.0-alpine3.18",
		ContainerPort:       "5432/tcp",
		DatabaseName:        databaseName,
		connOptions:         "",
		containerEnv: []string{
			"POSTGRES_USER=postgres",
			"POSTGRES_PASSWORD=postgres",
			fmt.Sprintf("POSTGRES_DB=%s", databaseName),
		},
		containerCmd:  []string{},
		containerName: "postgres_test",
		waitInterval:  200 * time.Millisecond,
		maxWaitTime:   time.Minute,
	}
}

type Option interface {
	apply(*Postgres)
}

type optionFunc func(*Postgres)

func (f optionFunc) apply(p *Postgres) {
	f(p)
}

func WithImageName(name string) Option {
	return optionFunc(func(p *Postgres) {
		p.ImageName = name
	})
}

func WithContainerPort(port nat.Port) Option {
	return optionFunc(func(p *Postgres) {
		p.ContainerPort = port
	})
}

func WithConnOptions(opts string) Option {
	return optionFunc(func(p *Postgres) {
		p.connOptions = opts
	})
}

func WithContainerEnv(env []string) Option {
	return optionFunc(func(p *Postgres) {
		p.containerEnv = env
	})
}

func WithContainerCmd(cmd []string) Option {
	return optionFunc(func(p *Postgres) {
		p.containerCmd = cmd
	})
}

func WithContainerName(name string) Option {
	return optionFunc(func(p *Postgres) {
		p.containerName = name
	})
}

func WithWaitInterval(interval time.Duration) Option {
	return optionFunc(func(p *Postgres) {
		p.waitInterval = interval
	})
}

func WithMaxWaitTime(interval time.Duration) Option {
	return optionFunc(func(p *Postgres) {
		p.maxWaitTime = interval
	})
}

// RunInDocker runs the tests with
// a mysql db instance in a docker container.
func (p *Postgres) RunInDocker(m *testing.M) int {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("panic", "err", err)
		}
	}()

	ctx := context.Background()
	cli, hostIP, bindingHostIP := p.Generate()

	// 根据镜像名,查询已存在的镜像
	filter := filters.NewArgs(filters.Arg("reference", p.ImageName))
	images, err := cli.ImageList(context.Background(), image.ListOptions{Filters: filter})
	if err != nil {
		panic(err)
	}

	// 如果没有这个镜像,则去pull
	if len(images) == 0 {
		// ImagePull 请求 docker 主机从远程注册表中提取镜像。
		// 如果操作未经授权，它会执行特权功能并再试一次。由调用者来处理 io.ReadCloser 并正确关闭它。
		out, err := cli.ImagePull(context.Background(), p.ImageName, image.PullOptions{})
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
		Image: p.ImageName, // 对应docker镜像的名字
		ExposedPorts: nat.PortSet{ // 内部要暴露端口列表(比如mysql:3306,redis:6379)
			p.ContainerPort: {},
		},

		// 配置环境变量
		Env: p.containerEnv,

		// cmd
		Cmd: p.containerCmd,
	},
		// 主机的配置。保存不可移植的配置信息。
		&container.HostConfig{
			// 暴露端口(容器)和主机之间的端口映射
			PortBindings: nat.PortMap{
				p.ContainerPort: []nat.PortBinding{
					{
						HostIP:   bindingHostIP, // 主机IP地址
						HostPort: "0",           // 主机端口号。0代表随机端口
					},
				},
			},
		}, nil, nil, p.containerName)
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

	handCtx, cancel := context.WithTimeout(context.Background(), p.maxWaitTime)
	ticker := time.NewTicker(p.waitInterval)
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
	hostPort := insRes.NetworkSettings.Ports[p.ContainerPort][0]
	postgresDSN = fmt.Sprintf("host=%s user=postgres password=postgres dbname=%s port=%s %s",
		hostIP, p.DatabaseName, hostPort.HostPort, p.connOptions)
	slog.Info("data source name", "dsn", postgresDSN)

	return m.Run()
}

func NewGormPostgresDB(ctx context.Context, opts ...gorm.Option) (*gorm.DB, error) {
	if postgresDSN == "" {
		return nil, fmt.Errorf("postgres dsn not set. Please run Postgres.RunInDocker in TestMain")
	}
	sqlDB, err := sql.Open("pgx", postgresDSN)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for end := false; !end; {
		select {
		case <-ctx.Done():
			end = true
		case <-ticker.C:
			err = sqlDB.PingContext(ctx)
			if err == nil {
				end = true
			}
		}
	}
	return gorm.Open(postgres.Open(postgresDSN), opts...)
}
