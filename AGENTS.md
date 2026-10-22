# Baseten CLI — agent notes

## Running inside a Baseten sandbox (cloud coding sessions)

When you run in a sandbox created by `baseten grid cloud-session` from a warm image of this repo:

- Your execution API is at `http://localhost:8080`, no auth from inside. `POST /process` with `{"command": "...", "keepAlive": true, "timeout": 0}` runs a process that also disables idle suspend while it runs; stop that process to let the sandbox sleep.
- Idle suspend freezes the sandbox; files and processes resume on the next request. Never busy-wait to stay awake: end the work instead, and state what must re-run on wake.
- The user's interactive harness lives in tmux session `code`. On reconnect, attach to it; only when it is gone, start a fresh one (`claude --continue` resumes the last conversation).
