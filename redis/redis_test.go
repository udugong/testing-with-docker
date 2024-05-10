package redis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/udugong/testing-with-docker"
)

func TestRedisWithDocker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cli, err := NewClient(ctx)
	require.NoError(t, err)

	key := "test:key"
	value := "hello world"
	cli.Set(context.Background(), key, value, 10*time.Second)
	v, err := cli.Get(context.Background(), key).Result()
	assert.NoError(t, err)
	assert.Equal(t, value, v)
}

func TestMain(m *testing.M) {
	os.Exit(New(dockertesting.NewLocalDockerItem(), WithImageName("redis:7")).RunInDocker(m))
}
