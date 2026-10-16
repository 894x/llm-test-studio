package remoteresults

// PriorityPage contains bounded active shared-job IDs used as scheduling hints.
// Terminal jobs leave fact priority; history still revisits them for late facts.
// The page does not authenticate or project remote execution facts.
type PriorityPage struct {
	JobIDs     []string
	NextCursor string
	HasMore    bool
}
