# Agent Guidance

## Jottacloud Token Handling

`JOTTA_TOKEN` is a Jottacloud personal login token used for one-time login during container setup. In this repo's workflows it is typically short-lived or one-time use, but real token values must still be treated as sensitive.

Do not commit, log, print, or copy actual `JOTTA_TOKEN` values unless required for the task. If a real token value is exposed accidentally, flag it during review unless it is clearly a placeholder or example. Keep normal caution for other credentials, session files, API keys, and persisted `/data/jottad` state.