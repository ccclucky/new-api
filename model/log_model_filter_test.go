package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupLogFilterTestDB(t *testing.T) {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	database, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&Log{}))
	DB, LOG_DB = database, database
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, database.Create(&Log{
		UserId: 1, Type: LogTypeConsume, ModelName: "claude-sonnet-4.5", CreatedAt: 3,
		Other: `{"cache_tokens":0,"smart_routing":{"by":"jev","from":"auto","to":"claude-sonnet-4.5"}}`,
	}).Error)
	require.NoError(t, database.Create(&Log{
		UserId: 1, Type: LogTypeConsume, ModelName: "gpt-5.1", CreatedAt: 2,
		Other: `{"cache_tokens":0}`,
	}).Error)
	require.NoError(t, database.Create(&Log{
		UserId: 1, Type: LogTypeConsume, ModelName: "auto", CreatedAt: 1,
		Other: `{"cache_tokens":0}`,
	}).Error)
}

func useAutoRoutingSettings(t *testing.T, enabled bool, virtualModel string) {
	t.Helper()
	current := operation_setting.GetAutoRoutingSetting()
	previous := *current
	t.Cleanup(func() { *current = previous })
	current.Enabled = enabled
	current.VirtualModel = virtualModel
}

func TestGetAllLogsRequestedModelFilterMatchesRoutedLogs(t *testing.T) {
	setupLogFilterTestDB(t)
	useAutoRoutingSettings(t, true, "auto")

	// The virtual name selects every routed log by its summary, not the
	// stored model_name.
	logs, total, err := GetAllLogs(LogTypeConsume, 0, 0, "auto", "", "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	assert.Equal(t, "claude-sonnet-4.5", logs[0].ModelName)

	// The explicit sentinel behaves identically.
	logs, total, err = GetAllLogs(LogTypeConsume, 0, 0, SmartRoutingModelFilter, "", "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	assert.Equal(t, "claude-sonnet-4.5", logs[0].ModelName)
}

func TestGetAllLogsRequestedModelFilterKeepsOrdinaryMatching(t *testing.T) {
	setupLogFilterTestDB(t)
	useAutoRoutingSettings(t, true, "auto")

	// Real model names keep exact matching.
	_, total, err := GetAllLogs(LogTypeConsume, 0, 0, "gpt-5.1", "", "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// With routing disabled, the old virtual name is an ordinary model name.
	useAutoRoutingSettings(t, false, "auto")
	_, total, err = GetAllLogs(LogTypeConsume, 0, 0, "auto", "", "", 0, 10, 0, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
}

func TestGetUserLogsRequestedModelFilterMatchesRoutedLogs(t *testing.T) {
	setupLogFilterTestDB(t)
	useAutoRoutingSettings(t, true, "smart")

	logs, total, err := GetUserLogs(1, LogTypeConsume, 0, 0, "smart", "", 0, 10, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	assert.Equal(t, "claude-sonnet-4.5", logs[0].ModelName)
}
