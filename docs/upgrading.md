# Upgrading from the TypeScript version (0.x)

`orbit.yaml`, the commands and the keys are the same. What changed under the hood is the socket protocol, so an
old daemon cannot be talked to by the new binary: run `orbit down` with the old version before
switching, or the new one will tell you which version the daemon runs. Services the old version left running are
picked up again: the per-project `state.json`, logs and lock keep their layout.
