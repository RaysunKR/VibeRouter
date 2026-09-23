package logger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDailyRotationAndRetention(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "router.log")

	l, err := Open(base, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	// 写入今天的日志
	l.Log(Entry{RequestModel: "today", Status: 200, KeyName: "k1"})
	if _, err := os.Stat(l.dailyPath(time.Now())); err != nil {
		t.Fatalf("daily file not created: %v", err)
	}
	// 单文件 router.log 不应存在
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatal("base file should not exist; logs go to daily files")
	}

	// 伪造 8 天前和 3 天前的日志文件
	old := l.dailyPath(time.Now().AddDate(0, 0, -8))
	recent := l.dailyPath(time.Now().AddDate(0, 0, -3))
	_ = os.WriteFile(old, []byte(`{"ts":"2026-08-07T00:00:00+08:00","request_model":"old","status":200}`+"\n"), 0o600)
	_ = os.WriteFile(recent, []byte(`{"ts":"2026-08-12T00:00:00+08:00","request_model":"recent","status":200,"key_name":"k2"}`+"\n"), 0o600)

	l.purgeOld()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("8-day-old log should be purged")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatal("3-day-old log should be kept")
	}

	// 查询应跨天合并
	entries, err := l.Query(QueryFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]bool{}
	for _, e := range entries {
		models[e.RequestModel] = true
	}
	if !models["today"] || !models["recent"] {
		t.Fatalf("query should merge daily files: %v", models)
	}
	if models["old"] {
		t.Fatal("purged day must not appear")
	}

	// key 过滤
	entries, _ = l.Query(QueryFilter{Limit: 100, Key: "k2"})
	if len(entries) != 1 || entries[0].KeyName != "k2" {
		t.Fatalf("key filter: %+v", entries)
	}
}

func TestRetentionConfigurable(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "router.log"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if l.retentionDays != DefaultRetentionDays {
		t.Fatalf("default retention = %d, want %d", l.retentionDays, DefaultRetentionDays)
	}

	// 缩短到 1 天：昨天的文件也应被清掉
	yesterday := l.dailyPath(time.Now().AddDate(0, 0, -1))
	_ = os.WriteFile(yesterday, []byte(`{"ts":"2026-08-14T00:00:00+08:00","request_model":"y","status":200}`+"\n"), 0o600)
	l.SetRetentionDays(1)
	if _, err := os.Stat(yesterday); !os.IsNotExist(err) {
		t.Fatal("retention=1 should purge yesterday's file")
	}
}
