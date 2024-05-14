# testing-with-docker

方便在 Go 项目中使用 Docker 对存储层运行测试

# go versions

`>=1.20`

# usage

下载安装：`go get github.com/udugong/testing-with-docker@latest`

- [生成 docker api 客户端以及启动容器所需项](#生成-docker-相关项)
- [gorm](#gorm-package)

- [mongo](#mongo-package)
- [redis](#redis-package)

# 生成 docker 相关项

目前提供了以下的使用方式：

- [本地 Docker](#本地-Docker)
- [SSH 远程 Docker](#SSH 远程 Docker)

## 本地 Docker

连接本地的 Docker 来启动相应镜像的容器

```go
import "github.com/udugong/testing-with-docker"

// 该方法提供了一个本地 Docker item 的生成器
dockertest.NewLocalDockerItem()
```

## SSH 远程 Docker

使用 SSH 协议连接远程的 Docker 来启动相应镜像的容器。

注意：

1. 远程主机需要开放 22 端口
2. 需要配置 ssh 免密钥登录
3. 使用该方式需要远程主机的 Docker 版本 `>=18.09`

```go
import "github.com/udugong/testing-with-docker"

// 该方法提供了一个远程 Docker item 的生成器
dockertest.NewSSHDockerItem("ssh://<user>@<host>") // 例如 ssh://ubuntu@192.168.1.2
```

# `gorm` package

该包可以根据相应数据库镜像启动对应的容器，并且提供初始化 `*gorm.DB`
的方法。使用方法参考 [mysql](https://github.com/udugong/testing-with-docker/blob/main/gorm/mysql/mysql_test.go)、[postgres](https://github.com/udugong/testing-with-docker/blob/main/gorm/postgres/postgres_test.go)。

# `mongo` package

该包可以启动 mongodb 的容器，并且提供初始化 `*mongo.Client`
的方法。使用方法参考 [mongo](https://github.com/udugong/testing-with-docker/blob/main/mongo/mongo_test.go)。

# `redis` package

该包可以启动 redis 的容器，并且提供初始化 `*redis.Client`
的方法。使用方法参考 [redis](https://github.com/udugong/testing-with-docker/blob/main/redis/redis_test.go)。