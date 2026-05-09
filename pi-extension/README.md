# pi-solo

Pi package for the Solo task tracker.

## Features

- `solo` tool for task, session, handoff, health, and recovery actions
- `/solo`, `/solo-pick`, `/solo-done`, and `/solo-errors` commands
- auto-init when running inside a git repo without an existing Solo database
- checkbox-style widget showing active, ready, and completed tasks
- automatic task context injection before agent work starts

## Install

### From npm

```bash
pi install npm:pi-solo
```

### From git

```bash
pi install git:github.com/your-org/your-repo
```

### From a local checkout

```bash
pi install ./pi-extension -l
```

## Package manifest

Pi loads this package through the `pi` manifest in `package.json`:

```json
{
  "keywords": ["pi-package"],
  "pi": {
    "extensions": ["./src/index.ts"]
  }
}
```

That makes the package compatible with `pi install`, git installs, and the pi package gallery.

## Development

```bash
npm install
npm run typecheck
npm run pack:check
```

## Publishing

At the time of setup, `npm view pi-solo` returned 404, so `pi-solo` appears available.

Release checklist:

```bash
cd pi-extension
npm install
npm run publish:check
npm publish
```

After publish, users can install with:

```bash
pi install npm:pi-solo
```

See [PUBLISHING.md](./PUBLISHING.md) for the full checklist.
