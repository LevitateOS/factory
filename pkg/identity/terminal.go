package identity

// TerminalStart is trusted web-to-host input. Browser handlers derive actor,
// project login and creation scope from the current authorized Soda session.
type TerminalStart struct {
	ConnectionID string `json:"connection_id"`
	ProjectID    string `json:"project_id"`
	ActorID      int64  `json:"actor_id,string"`
	Login        string `json:"login"`
	Scope        string `json:"scope"`
	Cols         int    `json:"cols"`
	Rows         int    `json:"rows"`
}
