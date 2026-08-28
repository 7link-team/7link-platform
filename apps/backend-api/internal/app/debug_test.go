package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyDebugDefaults(t *testing.T) {
	config := AppConfig{Debug: true}
	applyDebugDefaults(&config)
	require.Len(t, config.Database, 1)
	database := config.Database["default"]
	assert.Equal(t, "sqlite", database.Type)
	assert.Equal(t, "sqlite.db", database.Dsn)
	assert.True(t, database.Writable)
	assert.True(t, database.Readable)
}

func TestApplyDebugDefaultsDoesNotChangeNormalOrExplicitConfig(t *testing.T) {
	normal := AppConfig{}
	applyDebugDefaults(&normal)
	assert.Nil(t, normal.Database)
	assert.False(t, normal.GrpcServer.Reflection)

	explicit := AppConfig{Debug: true, Database: map[string]Database{
		"main": {Type: "postgres", Dsn: "configured"},
	}}
	applyDebugDefaults(&explicit)
	assert.Equal(t, map[string]Database{
		"main": {Type: "postgres", Dsn: "configured"},
	}, explicit.Database)
	assert.False(t, explicit.GrpcServer.Reflection)
}
