package tools

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type readCacheEntry struct {
	ModTime time.Time
}

// ReadCache 既是读缓存，也是 per-session 上下文，承载 model 和子 Agent 降级状态
type ReadCache struct {
	mu      sync.Mutex
	entries map[string]readCacheEntry

	Model          string // 当前会话使用的 LLM model（子 Agent 从此读取）
	subAgentFails  int32  // per-session 子 Agent 连续失败计数（atomic）
}

func NewReadCache() *ReadCache {
	return &ReadCache{entries: make(map[string]readCacheEntry)}
}

// ── 子 Agent 降级（per-session） ──

func (c *ReadCache) IncrSubAgentFail() {
	atomic.AddInt32(&c.subAgentFails, 1)
}

func (c *ReadCache) ResetSubAgentFail() {
	atomic.StoreInt32(&c.subAgentFails, 0)
}

func (c *ReadCache) ShouldDegradeSubAgent() bool {
	return atomic.LoadInt32(&c.subAgentFails) >= 3
}

func (c *ReadCache) GetModel() string {
	if c == nil || c.Model == "" {
		return "gpt-4o"
	}
	return c.Model
}

func (c *ReadCache) Check(absPath string, offset, limit int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fmt.Sprintf("%s:%d:%d", absPath, offset, limit)
	entry, ok := c.entries[key]
	if !ok {
		return false
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return false
	}
	return info.ModTime().Equal(entry.ModTime)
}

func (c *ReadCache) Mark(absPath string, offset, limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fmt.Sprintf("%s:%d:%d", absPath, offset, limit)
	info, err := os.Stat(absPath)
	if err != nil {
		return
	}
	c.entries[key] = readCacheEntry{ModTime: info.ModTime()}
}

// Invalidate 清除指定文件的所有缓存条目（文件被修改后调用）
func (c *ReadCache) Invalidate(absPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := absPath + ":"
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
}

// HasRead 检查文件是否在本轮 Agentic Loop 中被读取过
func (c *ReadCache) HasRead(absPath string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := absPath + ":"
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// GetReadTime 返回文件最后一次被读取时记录的 ModTime（用于竞态检测）
func (c *ReadCache) GetReadTime(absPath string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := absPath + ":"
	var latest time.Time
	for key, entry := range c.entries {
		if strings.HasPrefix(key, prefix) && entry.ModTime.After(latest) {
			latest = entry.ModTime
		}
	}
	return latest
}
