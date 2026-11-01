#!/usr/bin/env python3
"""Manage only the isolated local v2 test bot; never production Compose/cache."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

NAME = "gobard-v2-test"
PREVIOUS = NAME + "-previous"
VOLUME = NAME + "-cache"
PURPOSE = "v2-local-test"


def docker(*args, optional=False):
    result = subprocess.run(["docker", *args], capture_output=True, text=True, check=False)
    if result.returncode:
        if optional:
            return None
        # Docker errors can echo configuration; never print raw stderr/argv.
        raise RuntimeError("Docker operation failed: " + args[0])
    return result.stdout


def inspect(name):
    value = docker("container", "inspect", name, optional=True)
    if value is None:
        return None
    info = json.loads(value)[0]
    if info["Config"].get("Labels", {}).get("gobard.purpose") != PURPOSE:
        raise RuntimeError("Refusing to manage a container without the local-test label")
    return info


def ready():
    probe = 'import urllib.request; print(urllib.request.urlopen("http://127.0.0.1:8080/ready",timeout=3).read().decode())'
    value = docker("exec", NAME, "python3", "-c", probe, optional=True)
    if value is None:
        return None
    return json.loads(value)


def wait_ready():
    for _ in range(30):
        status = ready()
        if status and status.get("status") == "ok":
            return status
        time.sleep(1)
    raise RuntimeError("Local bot did not become ready within 30 readiness probes")


def prepare_cache(image, old):
    existing = docker("volume", "inspect", VOLUME, optional=True)
    if existing:
        labels = json.loads(existing)[0].get("Labels") or {}
        if labels.get("gobard.purpose") != PURPOSE:
            raise RuntimeError("Refusing an unlabeled cache volume")
    else:
        docker("volume", "create", "--label", "gobard.purpose=" + PURPOSE, VOLUME)
    mounts = ["--mount", "type=volume,src=" + VOLUME + ",dst=/test-cache"]
    if old:
        cache = next((m for m in old["Mounts"] if m["Destination"] == "/app/cache"), None)
        if cache is None:
            raise RuntimeError("Existing local bot has no cache mount")
        if cache["Type"] == "bind":
            source = Path(cache["Source"])
            if source.parent != Path("/tmp") or not source.name.startswith("gobard-v2-live-cache-") or source.is_symlink():
                raise RuntimeError("Refusing a non-test or symlink cache")
            if source.is_dir():
                mounts += ["--mount", "type=bind,src=" + str(source) + ",dst=/old-cache,readonly"]
        elif cache["Type"] != "volume" or cache.get("Name") != VOLUME:
            raise RuntimeError("Refusing an unrelated cache volume")
    # Copy complete test media only, never overwrite/delete either cache. The
    # old bot is stopped before this copy. Buffers are bounded by copyfileobj.
    copy = '''import os,re,shutil
from pathlib import Path
target=Path('/test-cache')
os.chown(target,1000,1000)
source=Path('/old-cache')
if source.is_dir():
 for entry in source.iterdir():
  dest=target/entry.name
  if re.fullmatch(r'[a-f0-9]{32}\\.webm',entry.name) and entry.is_file() and not entry.is_symlink() and not dest.exists():
   try:
    with entry.open('rb') as src, dest.open('xb') as dst: shutil.copyfileobj(src,dst,65536)
    os.chown(dest,1000,1000)
   except BaseException:
    dest.unlink(missing_ok=True)
    raise
'''
    docker("run", "--rm", "--read-only", "--user", "0:0", *mounts,
           "--entrypoint", "python3", image, "-B", "-c", copy)


def replace(image, env_file=None):
    old = inspect(NAME)
    previous = inspect(PREVIOUS)
    image_info = json.loads(docker("image", "inspect", image))[0]
    if image_info["Architecture"] != "amd64" or image_info["Os"] != "linux":
        raise RuntimeError("The test image must target linux/amd64")
    if old:
        status = ready() if old["State"]["Running"] else None
        if old["State"]["Running"] and status is None:
            raise RuntimeError("Cannot verify idle playback; stop the test bot explicitly before updating")
        if status and (status.get("dimensions", {}).get("busy_sessions") != 0 or status.get("dimensions", {}).get("active_delivery", 0)):
            raise RuntimeError("Idle playback is unconfirmed (including pause/preparation); stop the test bot explicitly before updating")
        values = old["Config"]["Env"]
        if any("\n" in value or "\r" in value for value in values):
            raise RuntimeError("Multiline environment is unsupported")
        environment = "\n".join(values) + "\n"
    else:
        if env_file is None:
            raise RuntimeError("First start requires --env-file with test credentials")
        path = Path(env_file)
        if path.stat().st_mode & 0o077 or path.stat().st_size > 65536:
            raise RuntimeError("Test env file must be private and at most 64 KiB")
        environment = path.read_text()
    fd, filename = tempfile.mkstemp(prefix="gobard-local-env-")
    renamed = False
    run_attempted = False
    try:
        with os.fdopen(fd, "w") as stream:
            stream.write(environment)
        if old:
            docker("stop", "--timeout", "30", NAME)
        prepare_cache(image, old)
        if previous:
            docker("rm", PREVIOUS)  # Never -v: named volume and media survive.
        if old:
            docker("rename", NAME, PREVIOUS)
            renamed = True
        run_attempted = True
        docker("run", "-d", "--name", NAME, "--init", "--restart", "no",
               "--read-only", "--user", "1000:1000", "--tmpfs", "/tmp:rw,noexec,nosuid,size=128m",
               "--mount", "type=volume,src=" + VOLUME + ",dst=/app/cache",
               "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
               "--memory", "1g", "--cpus", "2", "--pids-limit", "256",
               "--log-opt", "max-size=10m", "--log-opt", "max-file=3",
               "--label", "gobard.purpose=" + PURPOSE, "--env-file", filename, image_info["Id"])
        return wait_ready()
    except BaseException:
        # Remove only a replacement created by this invocation; preserve cache.
        if run_attempted and inspect(NAME):
            docker("rm", "-f", NAME, optional=True)
        if renamed:
            docker("rename", PREVIOUS, NAME)
        if old and old["State"]["Running"]:
            docker("start", NAME)
        raise
    finally:
        os.unlink(filename)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["start", "update", "stop", "status"])
    parser.add_argument("--image", default="gobard:local")
    parser.add_argument("--env-file")
    args = parser.parse_args()
    old = inspect(NAME)
    if args.action == "status":
        print(json.dumps({"running": bool(old and old["State"]["Running"]), "health": ready() if old and old["State"]["Running"] else None}))
    elif args.action == "stop":
        if old:
            docker("stop", "--timeout", "30", NAME)
        print("Local test bot stopped; cache preserved.")
    elif args.action == "start" and old and any(m.get("Name") == VOLUME for m in old["Mounts"]):
        docker("start", NAME)
        print(json.dumps(wait_ready()))
    else:
        print(json.dumps(replace(args.image, args.env_file)))


if __name__ == "__main__":
    try:
        main()
    except RuntimeError as error:
        print(str(error), file=sys.stderr)  # Only helper-owned safe diagnostics.
        sys.exit(1)
    except (OSError, ValueError, KeyError):
        # No raw Docker response, credential-bearing environment or traceback.
        print("Local test operation failed. Check the private env file, labeled test container, image and Docker availability.", file=sys.stderr)
        sys.exit(1)
