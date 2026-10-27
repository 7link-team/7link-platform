"""Exercise a packaged native service and Web without a development runtime."""
import argparse
import json
import os
from pathlib import Path
import queue
import secrets
import subprocess
import tempfile
import threading
import urllib.request


def verify(service, web, name):
    service, web = Path(service).resolve(), Path(web).resolve()
    assert service.is_file() and (web / "index.html").is_file()
    with tempfile.TemporaryDirectory(prefix="desktop-local-acceptance-") as directory:
        def start():
            environment = os.environ.copy()
            if os.name == "nt":
                # Prove the packaged service does not rely on compiler DLLs
                # found through the hosted runner's development-tool PATH.
                environment["PATH"] = str(Path(os.environ["SystemRoot"]) / "System32")
            process = subprocess.Popen(
                [str(service), "desktop", "--data-dir", directory, "--web-dir", str(web), "--name", name],
                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                text=True, encoding="utf-8", cwd=directory, env=environment,
                creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0,
            )
            events, errors = queue.Queue(), []

            def read_events():
                for line in process.stdout:
                    try:
                        event = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    if event.get("event") in ("setup", "ready", "error"):
                        events.put(event)
                events.put({"event": "error", "message": "service stdout closed"})

            def read_errors():
                for line in process.stderr:
                    errors.append(line)
                    if len(errors) > 30:
                        errors.pop(0)

            threading.Thread(target=read_events, daemon=True).start()
            threading.Thread(target=read_errors, daemon=True).start()
            return process, events, errors

        def next_event(events):
            event = events.get(timeout=120)
            assert event["event"] != "error", event
            return event

        def stop(process):
            process.stdin.close()
            try:
                assert process.wait(timeout=45) == 0, "service shutdown failed"
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
                raise AssertionError("parent EOF did not stop packaged service")
            finally:
                process.stdout.close()
                process.stderr.close()

        process, events, errors = start()
        try:
            assert next_event(events)["event"] == "setup"
            password = secrets.token_urlsafe(32) + "aA1!"
            process.stdin.write(json.dumps({"command": "setup", "login": "package-owner", "password": password}) + "\n")
            process.stdin.flush()
            password = ""
            ready = next_event(events)
            assert ready["event"] == "ready", ready
            origin = ready["origin"]
            with urllib.request.urlopen(origin + "/", timeout=20) as response:
                assert response.status == 200 and b"<html" in response.read().lower()
            with urllib.request.urlopen(origin + "/api/deployment", timeout=20) as response:
                assert response.status == 200 and name in response.read().decode()
        finally:
            stop(process)
        assert (Path(directory) / "application.sqlite").is_file()
        process, events, errors = start()
        try:
            restarted = next_event(events)
            assert restarted["event"] == "ready" and restarted["origin"] == origin, restarted
            # Extra commands must never block the parent EOF shutdown path.
            process.stdin.write('{"command":"setup"}\n')
            process.stdin.flush()
            assert next_event(events)["event"] == "ready"
        finally:
            stop(process)
        try:
            urllib.request.urlopen(origin + "/", timeout=2)
        except OSError:
            pass
        else:
            raise AssertionError("local listener survived parent shutdown")
    print("Packaged local service: first administrator, Web/API, restart persistence, stable origin and parent EOF PASS")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("service")
    parser.add_argument("web")
    parser.add_argument("--name", required=True)
    args = parser.parse_args()
    verify(args.service, args.web, args.name)
