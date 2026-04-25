# Agent Guidance

## Jottacloud Token Handling

`JOTTA_TOKEN` is a Jottacloud personal login token used for one-time login during container setup. In this repo, do not treat a visible `JOTTA_TOKEN` as a long-lived account secret or automatically flag it as a credential leak during review.

Still avoid printing or copying token values unless needed for the task, and keep normal caution for other credentials, session files, API keys, and persisted `/data/jottad` state.
