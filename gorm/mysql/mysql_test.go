package mysqltest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/udugong/testing-with-docker"
)

func TestMySQL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := NewGormMySQlDB(ctx)
	require.NoError(t, err)

	type Table struct {
		ID      uint   `gorm:"primarykey"`
		Content string `gorm:"size:16"`
	}
	err = db.AutoMigrate(&Table{})
	require.NoError(t, err)

	ctx1, cancel1 := context.WithTimeout(context.Background(), time.Second)
	data := Table{
		ID:      1,
		Content: "hello world",
	}
	err = db.WithContext(ctx1).Create(&data).Error
	cancel1()
	assert.NoError(t, err)

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	newData := Table{ID: 1}
	err = db.WithContext(ctx2).Take(&newData).Error
	cancel2()
	assert.NoError(t, err)
	assert.Equal(t, data.Content, newData.Content)
}

func TestMain(m *testing.M) {
	os.Exit(New(dockertest.NewLocalDockerItem(), "test_in_docker").RunInDocker(m))
}
