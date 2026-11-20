package jev

// Providers. Three of the four speak the same protocol - a state and a table of
// typed questions in, answers keyed by question id out - so they differ only in
// where the request goes and how the body is wrapped:
//
//	typesafe    POST {base}/v1/systemone                      Bearer
//	bailian     POST {base} (workspace host)                  Bearer + workspace
//	cloudflare  POST {base}/accounts/{id}/ai/run/@cf/cloudflare/clef-flash
//	openai      POST {base}/v1/decisions                      Bearer, a different body
//
// OpenAI's Decisions API is the odd one: questions are an array with a name, a
// predicate replaces noul, a choice carries {value, description} pairs, a score
// carries labelled levels, and the answers come back as an array rather than a
// map. That translation lives in openai.go; the other three share systemone.go.
const (
	ProviderTypeSafe   = "typesafe"
	ProviderBailian    = "bailian"
	ProviderCloudflare = "cloudflare"
	ProviderOpenAI     = "openai"
)

// MaxQuestions is the per-request ceiling each provider documents. Bailian says
// sixteen; the others allow more, but the smallest ceiling is what a caller can
// rely on across providers.
const MaxQuestions = 16

// normalise turns an empty or unknown provider name into TypeSafe, which is what
// this deployment has been talking to.
func normalise(p string) string {
	switch p {
	case ProviderBailian, ProviderCloudflare, ProviderOpenAI, ProviderTypeSafe:
		return p
	default:
		return ProviderTypeSafe
	}
}
