package factory

// ActivitySettings controls the local repository feed independently of execution authority.
type ActivitySettings struct {
	IssueCreated  bool `json:"issue_created"`
	PRCreated     bool `json:"pr_created"`
	VerifiedMerge bool `json:"verified_merge"`
}

func DefaultActivitySettings() ActivitySettings { return ActivitySettings{true, true, true} }

type ActivityItem struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Issue       string `json:"issue"`
	PR          string `json:"pr,omitempty"`
	CreatedUnix int64  `json:"created_unix"`
	Summary     string `json:"summary"`
	// URL is supplied by admitted host navigation; stored fact reads leave it empty.
	URL string `json:"url"`
}

// ActivityItemBound caps the rendered repository feed. The store reports
// truncation explicitly; consumers never mistake a capped feed for the
// complete history.
const ActivityItemBound = 60

// RepositoryActivity is the bounded readable feed for one repository:
// the display settings revision, the newest confirmed native-work items
// and the observation behind them. Each item's CreatedUnix is the
// timestamp of its confirming durable record; zero means the confirming
// record carries no timestamp and renders as unknown, never as 1970.
// ObservedUnix marks when the feed was assembled. Truncated reports
// whether the bound dropped entries.
type RepositoryActivity struct {
	Repository string `json:"repository"`
	// RepositoryPath is host navigation metadata, never a repository identity.
	RepositoryPath string           `json:"repository_path"`
	Revision       int64            `json:"revision"`
	Settings       ActivitySettings `json:"settings"`
	Items          []ActivityItem   `json:"items"`
	ObservedUnix   int64            `json:"observed_unix"`
	Truncated      bool             `json:"truncated,omitempty"`
}
