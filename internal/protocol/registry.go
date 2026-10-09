// Package protocol describes supported wire protocols without depending on
// catalog storage, execution, or desktop presentation.
package protocol

const (
	OpenAIChat   = "openai-chat"
	Seedance     = "seedance"
	WanVideo     = "wan-video"
	MiniMaxVideo = "minimax-video"

	OpenAIChatPath   = "/v1/chat/completions"
	OpenAIModelsPath = "/v1/models"
	SeedancePath     = "/api/v3/contents/generations/tasks"
	WanVideoPath     = "/api/v1/services/aigc/video-generation/video-synthesis"
	MiniMaxVideoPath = "/v2/video_generation"
)

type RequestPath struct {
	Operation string
	Path      string
}

// Descriptor contains protocol-wide behavior. Model-specific limits and
// request contracts remain in version-scoped Case definitions.
type Descriptor struct {
	ID           string
	Label        string
	Async        bool
	RequestPaths []RequestPath
}

var descriptors = [...]Descriptor{
	{ID: OpenAIChat, Label: "OpenAI Chat", RequestPaths: []RequestPath{
		{Path: OpenAIChatPath}, {Operation: "models.list", Path: OpenAIModelsPath},
	}},
	{ID: Seedance, Label: "Seedance", Async: true, RequestPaths: []RequestPath{{Path: SeedancePath}}},
	{ID: WanVideo, Label: "Wan Video", Async: true, RequestPaths: []RequestPath{{Path: WanVideoPath}}},
	{ID: MiniMaxVideo, Label: "MiniMax Video", Async: true, RequestPaths: []RequestPath{{Path: MiniMaxVideoPath}}},
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
