package capacity

import (
	"os"
	"strconv"
	"testing"
)

func TestMemoryDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		files  map[string]string
		want   int64
		source string
	}{
		{"missing", nil, FallbackMemory, "fallback"},
		{"host", map[string]string{"/proc/meminfo": "MemTotal: 2097152 kB\n"}, 2 << 30, "host"},
		{"container", map[string]string{
			"/proc/meminfo": "MemTotal: 67108864 kB", "/proc/self/cgroup": "0::/", "/proc/self/mountinfo": "1 2 0:3 /docker/id /sys/fs/cgroup rw - cgroup2 cgroup rw",
			"/sys/fs/cgroup/memory.max": "1073741824"}, 1 << 30, "cgroup"},
		{"nested ancestor", map[string]string{
			"/proc/self/cgroup": "0::/tenant/child", "/proc/self/mountinfo": "1 2 0:3 / /sys/fs/cgroup rw - cgroup2 cgroup rw",
			"/sys/fs/cgroup/tenant/child/memory.max": "max", "/sys/fs/cgroup/tenant/memory.max": "536870912", "/sys/fs/cgroup/memory.max": "2147483648"}, 512 * MiB, "cgroup"},
		{"high watermark", map[string]string{
			"/proc/self/cgroup": "0::/", "/proc/self/mountinfo": "1 2 0:3 / /sys/fs/cgroup rw - cgroup2 cgroup rw",
			"/sys/fs/cgroup/memory.max": "1073741824", "/sys/fs/cgroup/memory.high": "805306368"}, 768 * MiB, "cgroup"},
		{"v1 subtree", map[string]string{
			"/proc/self/cgroup": "4:cpu:/other\n5:memory:/tenant/child", "/proc/self/mountinfo": "1 2 0:3 /tenant /memory rw - cgroup cgroup rw,memory",
			"/memory/child/memory.limit_in_bytes": "9223372036854771712", "/memory/memory.limit_in_bytes": "1073741824"}, 1 << 30, "cgroup"},
		{"unlimited", map[string]string{
			"/proc/meminfo": "MemTotal: 2097152 kB", "/proc/self/cgroup": "0::/", "/proc/self/mountinfo": "1 2 0:3 / /sys/fs/cgroup rw - cgroup2 cgroup rw",
			"/sys/fs/cgroup/memory.max": "max"}, 2 << 30, "host"},
		{"malformed", map[string]string{"/proc/meminfo": "MemTotal: 9223372036854775807 kB", "/proc/self/cgroup": "bad", "/proc/self/mountinfo": "bad"}, FallbackMemory, "fallback"},
		{"zero limit", map[string]string{
			"/proc/self/cgroup": "0::/", "/proc/self/mountinfo": "1 2 0:3 / /sys/fs/cgroup rw - cgroup2 cgroup rw",
			"/sys/fs/cgroup/memory.max": "0"}, 1, "cgroup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := discover(func(name string) ([]byte, error) {
				value, ok := tc.files[name]
				if !ok {
					return nil, os.ErrNotExist
				}
				return []byte(value), nil
			})
			if got.Bytes != tc.want || got.Source != tc.source {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestCapacityHeadroomAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		ram  int64
		want int
	}{{512 * MiB, 1}, {1 << 30, 3}, {2 << 30, 3}, {4 << 30, 3}} {
		t.Run(strconv.FormatInt(tc.ram, 10), func(t *testing.T) {
			budget, err := Plan(Memory{tc.ram, "cgroup"}, 0, 0, 16, 4)
			if err != nil || budget.Streams != tc.want || budget.Reserved+int64(budget.Streams)*StreamMemory > tc.ram {
				t.Fatalf("%+v %v", budget, err)
			}
		})
	}
	for _, tc := range []struct {
		memory                   Memory
		override                 int64
		streams, guilds, workers int
		valid                    bool
	}{
		{Memory{1 << 30, "host"}, 512 * MiB, 1, 16, 4, true},
		{Memory{1 << 30, "cgroup"}, 2 << 30, 1, 16, 4, false},
		{Memory{FallbackMemory, "fallback"}, 1 << 30, 2, 16, 4, true},
		{Memory{256 * MiB, "cgroup"}, 0, 0, 16, 4, false},
		{Memory{1 << 30, "cgroup"}, 0, 5, 16, 4, false},
		{Memory{1 << 30, "cgroup"}, 0, 0, 1024, 4, false},
		{Memory{512 * MiB, "cgroup"}, 0, 0, 16, 16, false},
	} {
		_, err := Plan(tc.memory, tc.override, tc.streams, tc.guilds, tc.workers)
		if (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}
