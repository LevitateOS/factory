# Update and uninstall

Factory updates and uninstalls by naming the exact bundle it replaces, so nothing outside Factory's own files is touched.

## Update the app

Quit Factory, download and check the new archive as in [Install Factory](../01-Get-started/02-install.md), then pass the digest of the installed bundle's inventory:

```sh
installed=$(dirname "$(readlink -f ~/.local/bin/factory)")
./install.sh --upgrade-from "$(sha256sum "$installed/bundle.sha256" | cut -d' ' -f1)"
```

The installer verifies the installed version, installs the new one beside it and switches the launcher last. If it is interrupted, run the same command again to finish.

## Update the execution services

When a release changes the execution services, update them with the digest of their installed manifest. Quit Factory first:

```sh
sudo ./factory-runtime/execution/install.sh --user "$USER" \
  --upgrade-from "$(sudo sha256sum /var/lib/soda-factory-install/manifest | cut -d' ' -f1)"
```

## Uninstall the app

```sh
installed=$(dirname "$(readlink -f ~/.local/bin/factory)")
./install.sh --uninstall-from "$(sha256sum "$installed/bundle.sha256" | cut -d' ' -f1)"
```

This removes the launcher and menu entry. Your projects, drafts and keyring credentials stay, and so do the execution services.
