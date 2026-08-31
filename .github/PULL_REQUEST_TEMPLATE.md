## What this changes

<!-- The problem, and what this does about it. Describe everything inline: a
reviewer should not need to open anything else. -->

## How to test it

<!-- The commands or the steps. Include the PostgreSQL versions you used. -->

## Checklist

- [ ] One of the labels `release:patch`, `release:minor` or `release:major` is set
- [ ] Tests were written first, and cover the new behaviour
- [ ] `make lint test cover` passes
- [ ] Anything that changes structure or data shows a preview before running
- [ ] No secret is written in plain text, passed in `argv`, or logged
- [ ] README updated, with a usage example for anything newly visible
