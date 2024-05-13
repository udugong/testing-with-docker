package mysqltest

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/go-connections/nat"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/udugong/testing-with-docker"
)

var mysqlDSN string

type MySQL struct {
	dockertest.DockerItemGenerator
	ImageName     string
	ContainerPort nat.Port
	DatabaseName  string
	connOptions   string
	waitInterval  time.Duration // 等待容器启动的间隔
	containerEnv  []string
	containerCmd  []string
}

func New(generator dockertest.DockerItemGenerator, databaseName string, opts ...Option) *MySQL {
	res := defaultMySQL(generator, databaseName)
	for _, opt := range opts {
		opt.apply(res)
	}
	return res
}

func defaultMySQL(generator dockertest.DockerItemGenerator, databaseName string) *MySQL {
	return &MySQL{
		DockerItemGenerator: generator,
		ImageName:           "mysql:8.0.0",
		ContainerPort:       "3306/tcp",
		DatabaseName:        databaseName,
		connOptions:         "",
		waitInterval:        10 * time.Second,
		containerEnv: []string{
			"MYSQL_ALLOW_EMPTY_PASSWORD=true", // 允许mysql的密码为空
			// "MYSQL_ROOT_PASSWORD=123456",
			"TZ=Asia/Shanghai",
			fmt.Sprintf("MYSQL_DATABASE=%s", databaseName),
		},
		containerCmd: []string{
			"--character-set-server=utf8mb4",
			"--collation-server=utf8mb4_general_ci",
			"--explicit_defaults_for_timestamp=true",
			"--lower_case_table_names=1",
		},
	}
}

type Option interface {
	apply(*MySQL)
}

type optionFunc func(*MySQL)

func (f optionFunc) apply(s *MySQL) {
	f(s)
}

func WithImageName(name string) Option {
	return optionFunc(func(s *MySQL) {
		s.ImageName = name
	})
}

func WithContainerPort(port nat.Port) Option {
	return optionFunc(func(s *MySQL) {
		s.ContainerPort = port
	})
}

func WithWaitInterval(interval time.Duration) Option {
	return optionFunc(func(s *MySQL) {
		s.waitInterval = interval
	})
}

func WithContainerEnv(env []string) Option {
	return optionFunc(func(s *MySQL) {
		s.containerEnv = env
	})
}

func WithContainerCmd(cmd []string) Option {
	return optionFunc(func(s *MySQL) {
		s.containerCmd = cmd
	})
}

func WithConnOptions(opts string) Option {
	return optionFunc(func(s *MySQL) {
		s.connOptions = opts
	})
}

// RunInDocker runs the tests with
// a mysql db instance in a docker container.
func (s *MySQL) RunInDocker(m *testing.M) int {
	ctx := context.Background()
	cli, hostIP, bindingHostIP := s.Generate()

	// 根据镜像名,查询已存在的镜像
	filter := filters.NewArgs(filters.Arg("reference", s.ImageName))
	images, err := cli.ImageList(context.Background(), image.ListOptions{Filters: filter})
	if err != nil {
		panic(err)
	}

	// 如果没有这个镜像,则去pull
	if len(images) == 0 {
		// ImagePull 请求 docker 主机从远程注册表中提取镜像。
		// 如果操作未经授权，它会执行特权功能并再试一次。由调用者来处理 io.ReadCloser 并正确关闭它。
		out, err := cli.ImagePull(context.Background(), s.ImageName, image.PullOptions{})
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
		Image: s.ImageName, // 对应docker镜像的名字
		ExposedPorts: nat.PortSet{ // 内部要暴露端口列表(比如mysql:3306,redis:6379)
			s.ContainerPort: {},
		},

		// 配置环境变量
		Env: s.containerEnv,

		// cmd
		Cmd: s.containerCmd,
	},
		// 主机的配置。保存不可移植的配置信息。
		&container.HostConfig{
			// 暴露端口(容器)和主机之间的端口映射
			PortBindings: nat.PortMap{
				s.ContainerPort: []nat.PortBinding{
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

	time.Sleep(s.waitInterval)

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
	hostPort := insRes.NetworkSettings.Ports[s.ContainerPort][0]
	mysqlDSN = fmt.Sprintf("root:@tcp(%s:%s)/%s?%s",
		hostIP, hostPort.HostPort, s.DatabaseName, s.connOptions)

	return m.Run()
}

func NewGormMySQlDB(ctx context.Context) (*gorm.DB, error) {
	if mysqlDSN == "" {
		return nil, fmt.Errorf("mysql dsn not set. Please run MySQL.RunInDocker in TestMain")
	}
	sqlDB, err := sql.Open("mysql", mysqlDSN)
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
	db, err := gorm.Open(mysql.Open(mysqlDSN))
	if err != nil {
		return nil, err
	}
	return db.Debug(), nil
}
