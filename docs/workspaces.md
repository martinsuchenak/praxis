# Workspaces

Workspaces give bots access to external project directories. Without a workspace, bots are fully isolated in their own directory.

## workspaces.json

Create a `workspaces.json` file in the project root (see `workspaces.example.json`):

```json
{
  "myapp": {
    "path": "/home/user/projects/myapp",
    "gossip_secret": "myapp-secret",
    "default_scope": "isolated"
  },
  "website": {
    "path": "/home/user/projects/website",
    "default_scope": "isolated"
  }
}
```

Fields per workspace:

| Field | Required | Description |
|---|---|---|
| `path` | yes | Absolute host path to the project directory |
| `gossip_secret` | no | Authentication secret for bots in this workspace. Overrides `BOT_GLOBAL_SECRET`. Bots with different secrets drop each other's messages. |
| `default_scope` | no | Default communication scope for bots in this workspace (default: `open`). Can be overridden per-bot at spawn time. |

## Spawning with a Workspace

```bash
praxis spawn DevBot "Refactor authentication" --workspace myapp
```

The watchdog resolves the workspace name to its host path at spawn time and stores it in `status.json`. The path is added to `--allowed-paths` when the bot starts and mounted inside the bwrap sandbox.

Bots can then access workspace files using their own tools (`read_file`, `write_file`, `search`, `shell`) using the same paths as on the host.

## Workspace Inheritance

Children automatically inherit their parent's workspace. When a bot spawns a child via `spawn_bot`, the watchdog detects the parent-child relationship and copies the workspace path to the child. Bots cannot change their workspace or spawn into a different one.

## Communication Scope

The `default_scope` in `workspaces.json` sets the default peer visibility for all bots in a workspace. It can be overridden per-bot at spawn time with `--scope`.

See [networking.md](networking.md#communication-scope) for full scope semantics.

## Cross-Workspace Messaging

Gateway-scoped bots can send messages to bots in other workspaces. The watchdog relays the message after validating that the target workspace is in the sender's `allowed_workspaces` list.

```bash
praxis spawn Coordinator "Coordinate frontend and backend" \
  --workspace myapp --scope gateway --allowed-workspaces website
```

## Export / Import with Workspace Remapping

When exporting a bot that has a workspace, the host path is embedded in its `status.json`. On import to another machine, remap it:

```bash
praxis import explorer.tar.gz --workspace myapp=/home/newuser/projects/myapp
```

Multiple `--workspace` flags are supported for bots in a gateway scope with multiple allowed workspaces.
