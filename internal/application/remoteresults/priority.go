package remoteresults

// PriorityPage contains bounded active shared-job or unimported-document IDs
// used only as scheduling hints. Terminal jobs leave priority after importing
// known documents; history synchronization still revisits them for late data.
// The page does not authenticate or project remote execution facts.
type PriorityPage struct {
	JobIDs     []string
	NextCursor string
	HasMore    bool
}
