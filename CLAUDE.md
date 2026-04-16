# Branch Strategy

This repository uses a simplified Git Flow model.

## Long-lived branches
- `master` is the production branch.
- `dev` is the integration branch for daily development.

## Working branches
- Create feature work on `feat/*` from `dev`.
- Create bug-fix work on `fix/*` from `dev`.
- Merge `feat/*` and `fix/*` back into `dev` first.
- Promote changes from `dev` to `master` through a pull request.

## Protection and deployment rules
- Never push directly to `master`.
- `master` must only be updated by a `dev -> master` pull request.
- The k3s prod environment deploys the latest `master`.
- The k3s dev environment deploys the latest `dev`.

## Worktree workflow
```bash
git fetch origin
git worktree add ../ppanel-node-dev dev
git worktree add -b feat/your-change ../ppanel-node-feat dev
```
