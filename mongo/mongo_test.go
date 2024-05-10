package mgo

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	dockertesting "github.com/udugong/testing-with-docker"
)

func TestMongoDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cli, err := NewClient(ctx)
	require.NoError(t, err)

	type record struct {
		ID      primitive.ObjectID `bson:"_id"`
		Content string             `bson:"content"`
	}
	db := cli.Database("test_by_docker")
	col := db.Collection("test")

	content := "hello world"
	id, err := primitive.ObjectIDFromHex("62ebdde1ca4dbbee80fe5f87")
	require.NoError(t, err)

	r := record{
		ID:      id,
		Content: content,
	}
	_, err = col.InsertOne(context.Background(), r)
	require.NoError(t, err)

	res := col.FindOne(context.Background(), bson.M{"_id": id})
	err = res.Err()
	assert.NoError(t, err)

	var newR record
	err = res.Decode(&newR)
	assert.NoError(t, err)
	assert.Equal(t, r, newR)
}

func TestMain(m *testing.M) {
	os.Exit(New(dockertesting.NewLocalDockerItem()).RunInDocker(m))
}
