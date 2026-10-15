# Security policy

## Supported versions

Only the latest release gets security fixes.

## Reporting a vulnerability

Please do not open a public issue. Report it privately through
[GitHub's private vulnerability reporting](https://github.com/JGoterris/orbit/security/advisories/new), or by email
to contact@jgoterris.com.

Include what you found, how to reproduce it and the version (`orbit --version`) and platform. You should get an
answer within a week; once a fix is released, the advisory is published with credit to you unless you prefer
otherwise.

## Scope

orbit runs the commands written in `orbit.yaml` with your user's rights: a project you open can run anything, the
same as its `Makefile` or `package.json` scripts. Only open projects you trust. Issues of interest include another
local user reaching your orbit socket or named pipe, orbit running something the config did not ask for, and
problems in the release artifacts.
