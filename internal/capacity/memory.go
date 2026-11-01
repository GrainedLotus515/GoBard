// Package capacity derives conservative admission budgets from visible memory
// limits. It does not predict audibility or replace CPU/provider acceptance.
package capacity

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

const MiB int64 = 1 << 20
const FallbackMemory = 512 * MiB
const StreamMemory = 128 * MiB

type Memory struct {
	Bytes  int64
	Source string
}

func Discover() Memory { return discover(os.ReadFile) }

// Consider host RAM and every visible ancestor in each memory cgroup mount.
// A private cgroup namespace may hide ancestors; those require an operator
// budget. Missing/unlimited cgroups still retain the host RAM ceiling.
func discover(read func(string) ([]byte, error)) Memory {
	var result Memory
	include := func(n int64, source string) {
		if n > 0 && (result.Bytes == 0 || n < result.Bytes) {
			result = Memory{n, source}
		}
	}
	meminfo := optionalRead(read, "/proc/meminfo")
	for line := range strings.SplitSeq(string(meminfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "MemTotal:" && fields[2] == "kB" {
			n, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil && n > 0 && n <= (1<<63-1)/1024 {
				include(n*1024, "host")
			}
		}
	}
	groups := optionalRead(read, "/proc/self/cgroup")
	mounts := optionalRead(read, "/proc/self/mountinfo")
	for line := range strings.SplitSeq(string(mounts), "\n") {
		scanMount(read, line, string(groups), include)
	}
	if result.Bytes == 0 {
		return Memory{FallbackMemory, "fallback"}
	}
	return result
}

func optionalRead(read func(string) ([]byte, error), name string) []byte {
	data, err := read(name)
	if err != nil {
		return nil
	}
	return data
}

func scanMount(read func(string) ([]byte, error), line, groups string, include func(int64, string)) {
	parts := strings.Split(line, " - ")
	if len(parts) != 2 {
		return
	}
	left, right := strings.Fields(parts[0]), strings.Fields(parts[1])
	if len(left) < 5 || len(right) < 3 {
		return
	}
	v2 := right[0] == "cgroup2"
	if !v2 && (right[0] != "cgroup" || !hasField(right[2], "memory")) {
		return
	}
	root, mount := unescape(left[3]), unescape(left[4])
	for group := range strings.SplitSeq(groups, "\n") {
		fields := strings.SplitN(group, ":", 3)
		if len(fields) != 3 || (v2 && fields[1] != "") || (!v2 && !hasField(fields[1], "memory")) {
			continue
		}
		member := path.Clean(fields[2])
		var relative string
		switch {
		case member == "/": // namespace root corresponds to the mount root
		case member == root:
		case root == "/":
			relative = strings.TrimPrefix(member, "/")
		case strings.HasPrefix(member, root+"/"):
			relative = strings.TrimPrefix(member, root+"/")
		default:
			continue
		}
		files := []string{"memory.limit_in_bytes"}
		if v2 {
			files = []string{"memory.max", "memory.high"}
		}
		scanAncestors(read, mount, relative, files, include)
	}
}

func scanAncestors(read func(string) ([]byte, error), mount, relative string, files []string, include func(int64, string)) {
	for dir := path.Join(mount, relative); ; dir = path.Dir(dir) {
		for _, file := range files {
			data, err := read(path.Join(dir, file))
			if err != nil {
				continue
			}
			n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			// v1 uses a near-MaxInt64 sentinel for unlimited.
			if err == nil && n >= 0 && n < 1<<60 {
				include(max(n, 1), "cgroup")
			}
		}
		if dir == mount || dir == "/" {
			break
		}
	}
}

func hasField(list, value string) bool {
	for field := range strings.SplitSeq(list, ",") {
		if field == value {
			return true
		}
	}
	return false
}
func unescape(value string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
}

type Budget struct {
	Memory   Memory
	Reserved int64
	Streams  int
}

// Plan reserves heap/native/cache headroom, bounded metadata workers and
// retained guild state before assigning conservative per-stream allowances.
// Explicit values may reduce the detected budget, never overcommit it.
func Plan(memory Memory, override int64, streams, guilds, extractors int) (Budget, error) {
	if memory.Bytes <= 0 || override < 0 || streams < 0 || streams > 64 || guilds < 1 || guilds > 1024 || extractors < 1 || extractors > 16 {
		return Budget{}, fmt.Errorf("invalid capacity settings")
	}
	if override > 0 {
		if memory.Source != "fallback" && override > memory.Bytes {
			return Budget{}, fmt.Errorf("MEMORY_BUDGET exceeds visible memory limit")
		}
		memory = Memory{override, "operator"}
	}
	// Four MiB per retained guild covers 500 maximum-sized Track values,
	// tickets, queue snapshots and bounded Discord state with allocator slack.
	reserved := max(192*MiB, memory.Bytes/4) + int64(extractors)*32*MiB + int64(guilds)*4*MiB
	available := (memory.Bytes - reserved) / StreamMemory
	if memory.Bytes < reserved+StreamMemory {
		return Budget{}, fmt.Errorf("memory budget cannot safely admit one stream; reduce retained sessions or extractor concurrency, or increase the container memory limit")
	}
	// A long cold source can hold its extractor slot until near playback end.
	// Keep one slot for metadata when possible; warm fixtures alone hide this.
	limit := int(min(available, 64, int64(guilds), int64(max(1, extractors-1))))
	if streams > limit {
		return Budget{}, fmt.Errorf("MAX_ACTIVE_STREAMS exceeds memory/extractor-derived capacity of %d", limit)
	}
	if streams > 0 {
		limit = streams
	}
	return Budget{memory, reserved, limit}, nil
}
