package dockertesting

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/docker/cli/cli/connhelper"
	"github.com/docker/docker/client"
)

type DockerItemGenerator interface {
	Generate() (cli *client.Client, hostIP string, bindingIP string)
}

type LocalDockerItem struct{}

func NewLocalDockerItem() *LocalDockerItem {
	return &LocalDockerItem{}
}

func (l *LocalDockerItem) Generate() (cli *client.Client, hostIP string, bindingIP string) {
	hostIP, bindingIP = "127.0.0.1", "127.0.0.1"
	var err error
	cli, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}
	return
}

type SSHDockerItem struct {
	// DaemonURL ssh://<user>@<host> URL requires Docker 18.09 or later on the remote host.
	// If DaemonURL != "ssh://..." use local Docker.
	DaemonURL string
}

func NewSSHDockerItem(daemonURL string) *SSHDockerItem {
	return &SSHDockerItem{DaemonURL: daemonURL}
}

func (d *SSHDockerItem) Generate() (cli *client.Client, hostIP string, bindingIP string) {
	if urlSli := strings.SplitN(d.DaemonURL, "://", 2); urlSli[0] != "ssh" {
		panic("daemonURL must start with 'ssh'")
	}
	u, err := url.Parse(d.DaemonURL)
	if err != nil {
		panic(err)
	}
	hostIP = u.Hostname()
	cli, err = DockerClientFromSSH(d.DaemonURL)
	if err != nil {
		panic(err)
	}
	return
}

// DockerClientFromSSH Obtain the Docker client through SSH.
func DockerClientFromSSH(daemonURL string) (*client.Client, error) {
	// 使用golang的Docker SDK,通过ssh远程访问docker守护进程
	helper, err := connhelper.GetConnectionHelper(daemonURL)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: helper.Dialer,
		},
	}
	return client.NewClientWithOpts(client.FromEnv,
		client.WithHTTPClient(httpClient),
		client.WithHost(helper.Host),
		client.WithDialContext(helper.Dialer),
		client.WithAPIVersionNegotiation(), // 版本协商
	)
}
