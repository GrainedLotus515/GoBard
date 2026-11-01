package capacity

import (
	"errors"
	"io/fs"
	"testing"
)

const testMeminfo = "MemTotal:       33554432 kB\nMemFree:        1024 kB\n"

func memoryFixture(t *testing.T, files map[string]string) func(string) ([]byte, error) {
	t.Helper()
	return func(name string) ([]byte, error) {
		if name == "/proc/meminfo" {
			return []byte(testMeminfo), nil
		}
		if data, ok := files[name]; ok {
			return []byte(data), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestDetectMemoryLimitCgroupV2(t *testing.T) {
	readFile := memoryFixture(t, map[string]string{
		"/proc/self/cgroup":                     "0::/docker/test\n",
		"/proc/self/mountinfo":                  "26 23 0:26 / /sys/fs/cgroup rw,relatime - cgroup2 cgroup rw\n",
		"/sys/fs/cgroup/docker/test/memory.max": "1073741824\n",
	})
	limit, err := detectMemoryLimit(readFile)
	if err != nil || limit.Bytes != 1<<30 || limit.Source != "cgroup-v2" {
		t.Fatalf("limit = %+v, %v", limit, err)
	}
}

func TestDetectMemoryLimitCgroupV1NestedMount(t *testing.T) {
	readFile := memoryFixture(t, map[string]string{
		"/proc/self/cgroup":                                "4:cpu,cpuacct:/other\n3:memory:/docker/test\n",
		"/proc/self/mountinfo":                             "31 23 0:31 /docker /sys/fs/cgroup/memory rw,relatime - cgroup cgroup rw,memory\n",
		"/sys/fs/cgroup/memory/test/memory.limit_in_bytes": "2147483648\n",
	})
	limit, err := detectMemoryLimit(readFile)
	if err != nil || limit.Bytes != 2<<30 || limit.Source != "cgroup-v1" {
		t.Fatalf("limit = %+v, %v", limit, err)
	}
}

func TestDetectMemoryLimitFallsBackToHost(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no controller": {},
		"unlimited": {
			"/proc/self/cgroup":         "0::/\n",
			"/proc/self/mountinfo":      "26 23 0:26 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n",
			"/sys/fs/cgroup/memory.max": "max\n",
		},
		"v1 sentinel": {
			"/proc/self/cgroup":                           "3:memory:/\n",
			"/proc/self/mountinfo":                        "31 23 0:31 / /sys/fs/cgroup/memory rw - cgroup cgroup rw,memory\n",
			"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			limit, err := detectMemoryLimit(memoryFixture(t, files))
			if err != nil || limit.Bytes != 33554432*1024 || limit.Source != "host" {
				t.Fatalf("limit = %+v, %v", limit, err)
			}
		})
	}
}

func TestDetectMemoryLimitRejectsInvalidInput(t *testing.T) {
	readFile := memoryFixture(t, map[string]string{
		"/proc/self/cgroup":         "0::/\n",
		"/proc/self/mountinfo":      "26 23 0:26 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n",
		"/sys/fs/cgroup/memory.max": "zero\n",
	})
	if _, err := detectMemoryLimit(readFile); err == nil {
		t.Fatal("malformed cgroup limit accepted")
	}
	if _, err := detectMemoryLimit(func(string) ([]byte, error) { return nil, errors.New("read failed") }); err == nil {
		t.Fatal("host read failure ignored")
	}
}

func TestGuildLimitUsesMemoryAndConfiguredBounds(t *testing.T) {
	for _, test := range []struct {
		name       string
		limit      uint64
		configured int
		expected   int
	}{
		{"small", 1 << 30, 16, 16},
		{"medium configured cap", 2 << 30, 16, 16},
		{"large configured cap", 4 << 30, 32, 32},
		{"reduced memory", 512 * mib, 16, 5},
		{"count cap", 4 << 30, 2, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := GuildLimit(test.limit, DefaultHeadroom(test.limit), DefaultPerGuildReservation, test.configured)
			if err != nil || got != test.expected {
				t.Fatalf("limit = %d, %v; want %d", got, err, test.expected)
			}
		})
	}
}

func TestGuildLimitRejectsInsufficientOrInvalidBudget(t *testing.T) {
	for _, test := range []struct {
		limit, headroom, perGuild uint64
		configured                int
	}{
		{256 * mib, 256 * mib, DefaultPerGuildReservation, 16},
		{1 << 30, 1 << 30, DefaultPerGuildReservation, 16},
		{1 << 30, 0, 0, 16},
		{1 << 30, 0, DefaultPerGuildReservation, 0},
		{1 << 30, 0, DefaultPerGuildReservation, 1025},
	} {
		if _, err := GuildLimit(test.limit, test.headroom, test.perGuild, test.configured); err == nil {
			t.Fatalf("accepted invalid budget: %+v", test)
		}
	}
}
