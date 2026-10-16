package remoteresults

// PriorityPage contains bounded local submission IDs used only as scheduling
// hints. It does not authenticate or project any remote execution facts.
type PriorityPage struct {
	JobIDs     []string
	NextCursor string
	HasMore    bool
}
