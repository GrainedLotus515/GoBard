// Package capacity discovers memory budgets and derives bounded playback admission.
package capacity

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"strconv"
	"strings"
)

const (
	mib = uint64(1024 * 1024)
	// DefaultPerGuildReservation covers one uncached stream and its bounded
	// session state. It is rounded above observed concurrent acquisition costs.
	DefaultPerGuildReservation = 48 * mib
	defaultMinimumHeadroom     = 256 * mib
)

type MemoryLimit struct {
	Bytes  uint64
	Source string
}

func DefaultHeadroom(limit uint64) uint64 {
	return max(defaultMinimumHeadroom, limit/4)
}

func GuildLimit(limit, headroom, perGuild uint64, configured int) (int, error) {
	if configured < 1 || configured > 1024 || perGuild == 0 {
		return 0, errors.New("invalid playback capacity configuration")
	}
	if limit <= headroom || limit-headroom < perGuild {
		return 0, errors.New("memory budget cannot fund one playback guild")
	}
	available := (limit - headroom) / perGuild
	if available > 1024 {
		available = 1024
	}
	return min(configured, int(available)), nil
}

func DetectMemoryLimit() (MemoryLimit, error) {
	return detectMemoryLimit(os.ReadFile)
}

func detectMemoryLimit(readFile func(string) ([]byte, error)) (MemoryLimit, error) {
	host, err := readHostMemory(readFile)
	if err != nil {
		return MemoryLimit{}, err
	}
	cgroupData, err := readFile("/proc/self/cgroup")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return MemoryLimit{Bytes: host, Source: "host"}, nil
		}
		return MemoryLimit{}, fmt.Errorf("read process cgroup: %w", err)
	}
	kind, processPath := cgroupProcessPath(string(cgroupData))
	if kind == "" {
		return MemoryLimit{Bytes: host, Source: "host"}, nil
	}
	mountData, err := readFile("/proc/self/mountinfo")
	if err != nil {
		return MemoryLimit{}, fmt.Errorf("read cgroup mounts: %w", err)
	}
	mountPoint, mountRoot, err := cgroupMount(string(mountData), kind)
	if err != nil {
		return MemoryLimit{}, err
	}
	filename := "memory.max"
	if kind == "memory" {
		filename = "memory.limit_in_bytes"
	}
	limitPath := path.Join(mountPoint, cgroupRelativePath(mountRoot, processPath), filename)
	data, err := readFile(limitPath)
	if err != nil {
		return MemoryLimit{}, fmt.Errorf("read %s memory limit: %w", kind, err)
	}
	limit, finite, err := parseCgroupLimit(string(data))
	if err != nil {
		return MemoryLimit{}, fmt.Errorf("parse %s memory limit: %w", kind, err)
	}
	if finite && limit < host {
		source := "cgroup-v2"
		if kind == "memory" {
			source = "cgroup-v1"
		}
		return MemoryLimit{Bytes: limit, Source: source}, nil
	}
	return MemoryLimit{Bytes: host, Source: "host"}, nil
}

func readHostMemory(readFile func(string) ([]byte, error)) (uint64, error) {
	data, err := readFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read host memory: %w", err)
	}
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "MemTotal:" || fields[2] != "kB" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || value == 0 || value > math.MaxUint64/1024 {
			return 0, errors.New("invalid MemTotal")
		}
		return value * 1024, nil
	}
	return 0, errors.New("MemTotal not found")
}

func cgroupProcessPath(data string) (string, string) {
	for line := range strings.Lines(data) {
		line = strings.TrimSpace(line)
		parts := strings.Split(line, ":")
		if len(parts) != 3 || !strings.HasPrefix(parts[2], "/") {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			return "cgroup2", parts[2]
		}
		for controller := range strings.SplitSeq(parts[1], ",") {
			if controller == "memory" {
				return "memory", parts[2]
			}
		}
	}
	return "", ""
}

func cgroupMount(data, kind string) (string, string, error) {
	for line := range strings.Lines(data) {
		left, right, found := strings.Cut(line, " - ")
		if !found {
			continue
		}
		rightFields := strings.Fields(right)
		leftFields := strings.Fields(left)
		if len(rightFields) < 3 || len(leftFields) < 5 {
			continue
		}
		matches := kind == "cgroup2" && rightFields[0] == "cgroup2"
		if kind == "memory" && rightFields[0] == "cgroup" {
			for _, field := range append(leftFields[5:], rightFields[2:]...) {
				for option := range strings.SplitSeq(field, ",") {
					if option == "memory" {
						matches = true
					}
				}
			}
		}
		if matches {
			return leftFields[4], leftFields[3], nil
		}
	}
	return "", "", fmt.Errorf("%s memory cgroup mount not found", kind)
}

func cgroupRelativePath(root, processPath string) string {
	if root != "/" {
		if processPath == root {
			return "/"
		}
		if relative, found := strings.CutPrefix(processPath, root+"/"); found {
			return "/" + relative
		}
	}
	return processPath
}

func parseCgroupLimit(data string) (uint64, bool, error) {
	value := strings.TrimSpace(data)
	if value == "max" {
		return 0, false, nil
	}
	limit, err := strconv.ParseUint(value, 10, 64)
	if err != nil || limit == 0 {
		return 0, false, errors.New("expected a positive byte limit or max")
	}
	if limit >= 1<<60 {
		return 0, false, nil
	}
	return limit, true, nil
}
