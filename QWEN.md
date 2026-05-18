## vibeflow Agent Session Rules

**CRITICAL - When a vibeflow session_init prompt is active (autonomous agent mode), these rules apply to ALL work, including ad-hoc user requests:**

1. **NEVER write code before creating a tracked work item in vibeflow.** If the user asks you to build, fix, add, or modify anything, your FIRST action must be to classify it (feature todo or issue) and create it in vibeflow via the MCP tools. No exceptions.

2. **The ad-hoc request workflow in the agent prompt takes ABSOLUTE PRIORITY.** Do not begin implementation until after the vibeflow work item exists and has been transitioned to `implementing` status.

3. **Every piece of work must flow through vibeflow status transitions** (planning -> implementing -> done), with execution logs published, git commits tracked, and line counts passed - even for "small" or "quick" changes.

4. **When polling for work, always drill into features to check todos.** `list_features` returns containers, not work items. For each feature returned with `ready_to_implement` or `implementing` status, call `list_todos(feature_id, status: "ready_to_implement,implementing")` to find actual work items. Never treat an empty `list_issues` result as "no work" without also checking todos inside returned features.

5. **YOU MUST use filters for tool calls to optimize data fetch.** Example: when listing_features to find items ready for work, filter by status so you only get features that are ready.

6. **IMPORTANT: You must continue polling after active work items are complete and follow the session_init prompt instructions as exactly specified at all times.**

7. **When continuing from a summarized/compacted conversation**: If the conversation starts with a session continuation summary mentioning a vibeflow session, you MUST re-load the full agent prompt before resuming work. Do this by:
   a. Read `.vibeflow-session-{persona}` from the working directory to get the existing session_id (e.g., `.vibeflow-session-developer`, `.vibeflow-session-architect`)
   b. Call `session_init(project_name, session_id)` to get the full agent prompt
   c. Re-read the returned `prompt` field to reload Phase 1-4 instructions
   d. Skip Phase 1 steps already done (project lookup, etc.) but honor ALL behavioral rules from the prompt - especially Phase 4 context updates
   This prevents loss of Phase 4 context updates and other critical behaviors when conversations are compacted.

8. **ALWAYS use `wait_for_work` for polling.** The `wait_for_work` MCP tool is the required polling mechanism - it blocks efficiently until work is available, handles session heartbeats automatically, and supports receiving user prompts. Use `security_reviewed` and `qa_verified` filter params to only receive items relevant to your persona.

9. **ALWAYS pass session_id when calling session_init.** Before calling `session_init` — directly OR via a sub-agent (Agent tool) — you MUST first read the persona-specific session file from the working directory (e.g., `.vibeflow-session-architect`, `.vibeflow-session-ux_designer`). If it contains a valid ID (starts with `session-`), pass it as `session_id`. When using the Agent tool for initialization, read the file yourself FIRST and inject `session_id: <prior_id>` into the sub-agent's prompt before spawning it. NEVER construct a `session_init` call (or sub-agent prompt) without checking the session file first. The server-side lookup is a fallback only.

## vibeflow agent Non-Stop Polling Contract (MANDATORY)

After Phase 1, the agent MUST remain in a polling loop and MUST NOT end the turn because no work is available.

Polling call:
- Use `wait_for_work(sid: <sid>)` — the server uses the poll timeout stored on your session record. Do not pass an explicit `timeout_seconds`.

On return:
- If work/prompts exist: process them.
- If empty: immediately call `wait_for_work` again.
- If tool error contains timeout/deadline (`timed out`, `deadline has elapsed`): treat as an empty cycle and immediately retry `wait_for_work(sid: <sid>)`.

Stop conditions:
- Only stop loop if user explicitly says to stop/exit.
- Never send a "no work" completion message as final output.

Before sending any final response, verify:
1) loop_started == true
2) last action was either `wait_for_work` retry or work execution
If not, continue polling.

## Output Efficiency

- No hollow closings. No "I hope this helps!", "Let me know if you need anything!".
- No unsolicited suggestions. Do exactly what was asked, nothing more.
- Disagree when wrong. State the correction directly. Do not change a correct answer because the user pushes back.

## Accuracy

- Never speculate about code, files, or APIs you have not read.
- If unsure: say "I don't know." Never guess confidently.
- Never invent file paths, function names, or API signatures.
