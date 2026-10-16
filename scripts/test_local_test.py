import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("local_test", Path(__file__).with_name("local-test.py"))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)


class LocalTestSafety(unittest.TestCase):
    def test_unlabeled_container_is_rejected(self):
        with patch.object(helper, "docker", return_value=json.dumps([{"Config": {"Labels": {}}}])):
            with self.assertRaises(RuntimeError):
                helper.inspect(helper.NAME)

    def test_production_cache_is_never_mounted(self):
        calls = []
        def docker(*args, **kwargs):
            calls.append(args)
            if args[:2] == ("volume", "inspect"):
                return json.dumps([{"Labels": {"gobard.purpose": helper.PURPOSE}}])
            return ""
        old = {"Mounts": [{"Destination": "/app/cache", "Type": "bind", "Source": "/production/cache"}]}
        with patch.object(helper, "docker", side_effect=docker):
            with self.assertRaises(RuntimeError):
                helper.prepare_cache("image", old)
        self.assertFalse(any(call[0] == "run" for call in calls))

    def test_failed_update_restores_original_and_removes_private_env(self):
        old = {"Config": {"Env": ["DISCORD_TOKEN=secret"]}, "State": {"Running": True}}
        state = {helper.NAME: old}
        env_paths = []
        def docker(*args, **kwargs):
            if args[:2] == ("image", "inspect"):
                return json.dumps([{"Architecture": "amd64", "Os": "linux", "Id": "sha256:test"}])
            if args[0] == "rename":
                state[args[2]] = state.pop(args[1])
            if args[0] == "run":
                path = Path(args[args.index("--env-file") + 1])
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                self.assertIn("DISCORD_TOKEN=secret", path.read_text())
                env_paths.append(path)
                state[helper.NAME] = {"new": True}
            if args[0] == "rm":
                state.pop(helper.NAME, None)
            return ""
        with patch.object(helper, "inspect", side_effect=lambda name: state.get(name)), patch.object(helper, "docker", side_effect=docker), patch.object(helper, "ready", return_value={"dimensions": {"active_delivery": 0, "busy_sessions": 0}}), patch.object(helper, "prepare_cache"), patch.object(helper, "wait_ready", side_effect=RuntimeError("not ready")):
            with self.assertRaises(RuntimeError):
                helper.replace("image")
        self.assertIs(state[helper.NAME], old)
        self.assertEqual(len(env_paths), 1)
        self.assertFalse(env_paths[0].exists())

    def test_active_playback_is_preserved(self):
        old = {"State": {"Running": True}}
        with patch.object(helper, "inspect", return_value=old), patch.object(helper, "docker", return_value=json.dumps([{"Architecture": "amd64", "Os": "linux"}])), patch.object(helper, "ready", return_value={"dimensions": {"active_delivery": 1}}), patch.object(helper, "prepare_cache") as cache:
            with self.assertRaises(RuntimeError):
                helper.replace("image")
            cache.assert_not_called()

    def test_unknown_or_paused_preparing_playback_is_preserved(self):
        for busy in [None, -1, 1]:
            with self.subTest(busy=busy):
                old = {"State": {"Running": True}}
                health = {"dimensions": {"active_delivery": 0, "busy_sessions": busy}}
                with patch.object(helper, "inspect", return_value=old), patch.object(helper, "docker", return_value=json.dumps([{"Architecture": "amd64", "Os": "linux"}])), patch.object(helper, "ready", return_value=health), patch.object(helper, "prepare_cache") as cache:
                    with self.assertRaises(RuntimeError):
                        helper.replace("image")
                    cache.assert_not_called()

    def test_failed_first_start_removes_partial_container(self):
        state = {}
        def docker(*args, **kwargs):
            if args[:2] == ("image", "inspect"):
                return json.dumps([{"Architecture": "amd64", "Os": "linux", "Id": "sha256:test"}])
            if args[0] == "run":
                state[helper.NAME] = {"partial": True}
                raise RuntimeError("run failed after creation")
            if args[0] == "rm":
                state.pop(helper.NAME, None)
            return ""
        with tempfile.TemporaryDirectory() as directory:
            env = Path(directory) / "private.env"
            env.write_text("DISCORD_TOKEN=fixture\n")
            os.chmod(env, 0o600)
            with patch.object(helper, "inspect", side_effect=lambda name: state.get(name)), patch.object(helper, "docker", side_effect=docker), patch.object(helper, "prepare_cache"):
                with self.assertRaises(RuntimeError):
                    helper.replace("image", env)
            self.assertFalse(state)


if __name__ == "__main__":
    unittest.main()
