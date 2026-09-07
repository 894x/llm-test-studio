// Package protocol describes supported wire protocols without depending on
// catalog storage, execution, or desktop presentation.
package protocol

const (
	OpenAIChat   = "openai-chat"
	KimiK3       = "kimi-k3"
	Seedance     = "seedance"
	WanVideo     = "wan-video"
	MiniMaxVideo = "minimax-video"
)

// Descriptor contains protocol-wide behavior. Model-specific limits and
// request contracts remain in version-scoped Case definitions.
type Descriptor struct {
	ID                   string
	Label                string
	Async                bool
	RequiresModelTargets bool
}

var descriptors = [...]Descriptor{
	{ID: OpenAIChat, Label: "OpenAI Chat"},
	{ID: KimiK3, Label: "Kimi K3"},
	{ID: Seedance, Label: "Seedance", Async: true},
	{ID: WanVideo, Label: "Wan Video", Async: true, RequiresModelTargets: true},
	{ID: MiniMaxVideo, Label: "MiniMax Video", Async: true, RequiresModelTargets: true},
}

func All() []Descriptor { return append([]Descriptor(nil), descriptors[:]...) }

func Lookup(id string) (Descriptor, bool) {
	for _, descriptor := range descriptors {
		if descriptor.ID == id {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}
