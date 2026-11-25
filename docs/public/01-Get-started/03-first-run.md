# The first-run guide

On the first start Factory shows a four-step guide. You can stop after any step and pick it up again from Accounts.

## The four steps

1. **Start Factory on this computer.** Factory starts its local services. The step completes on its own; if the desktop keyring is locked, unlock it when your desktop asks.
2. **Sign in to your coding-agent service.** Factory opens the service's sign-in page in your browser and shows a short code to confirm there. The step finishes by itself once you confirm. The service keeps its own saved sign-in, so you rarely repeat this.
3. **Add your Git hosting account.** Sign in so Factory can read and write the repositories you choose. The account is saved in your desktop keyring, not in a file.
4. **Add your first project.** Pick a repository from your account, or open one by owner/name or repository URL. See [Add a project](04-add-a-project.md).

When the last step finishes, the guide closes and you land on Projects with your first project listed.

## What the side panel shows

"What this release offers" lists the coding-agent service and repository host this computer can use. Anything listed as unavailable stays off; Factory does not assume it works.

## Letting this computer run agents

Running agents on this computer is a separate choice from signing in. You make it in Settings, under Execution controller. See [Settings and accounts](../03-Settings/01-settings-and-accounts.md).
