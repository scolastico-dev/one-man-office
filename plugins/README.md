# omo's plugins

This folder holds omo's plugin list, `index.yaml`, and the official plugins.
The release workflow copies the folder to the `release` branch of the
repository; omo reads the list from there and shows it on the Plugins page
under "Available plugins". Plugins are not built into omo: every plugin,
official or not, is installed from a git source.

## The tags

| Tag | Meaning | Installing |
|---|---|---|
| `official` | Kept in this repository (a folder next to this file) by omo's maintainers, released with omo. | At once. |
| `recommended` | Someone else's plugin, in its own repository, that the maintainers looked at. | omo warns that it is someone else's code and asks first. |
| `community` | Listed, not reviewed. | omo warns more strongly and asks first. |

A plugin's hooks run commands on the user's computer with the user's rights,
whatever its tag. A source typed by hand counts as `community`.

## Getting a plugin listed

Open a pull request that adds an entry to `index.yaml`:

```yaml
  - name: my-plugin             # the plugin's name: letters, digits, . _ -
    tag: community              # recommended only after the maintainers looked at it
    description: One line on what it does, at most 200 characters.
    source: https://example.org/me/my-plugin.git   # https or ssh
    ref: v1.0.0                 # a tag or a full commit id, so the listed version stays put
    path: ""                    # the plugin's folder in the repository; empty for the top
```

- The plugin must load in the current omo: a `plugin.json` with name,
  version, description and hooks, and every Lua file it names.
- Say in the pull request what the plugin does, which events it uses, what it
  sends where and which programs it runs. For `recommended`, the maintainers
  read the code at the named `ref`; a new version needs a new review.
- An official plugin is a folder here with only `path` in its entry (no
  `source` or `ref`: it installs from the repository and branch the list is
  read from). The list and every official manifest are checked by
  `go test ./internal/pluginmanager -run TestShippedCatalog`.

omo refuses the whole list when one entry is wrong (an unknown key or tag, a
repeated name, an address omo does not fetch from, a description of more than
one line), so check it before the pull request.
