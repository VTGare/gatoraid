package translate

type Language struct {
	Code string
	Name string
}

// Common are the targets offered in /settings. A Discord select holds 25
// options. Other DeepL targets can be typed in.
var Common = []Language{
	{"EN-US", "English (American)"},
	{"EN-GB", "English (British)"},
	{"JA", "Japanese"},
	{"KO", "Korean"},
	{"ZH-HANS", "Chinese (simplified)"},
	{"ZH-HANT", "Chinese (traditional)"},
	{"ID", "Indonesian"},
	{"MS", "Malay"},
	{"TH", "Thai"},
	{"VI", "Vietnamese"},
	{"TL", "Tagalog"},
	{"ES", "Spanish"},
	{"ES-419", "Spanish (Latin American)"},
	{"PT-BR", "Portuguese (Brazilian)"},
	{"PT-PT", "Portuguese (European)"},
	{"FR", "French"},
	{"DE", "German"},
	{"IT", "Italian"},
	{"NL", "Dutch"},
	{"PL", "Polish"},
	{"RU", "Russian"},
	{"UK", "Ukrainian"},
	{"TR", "Turkish"},
	{"AR", "Arabic"},
	{"SV", "Swedish"},
}

// Name is the language's English name, or the code itself for languages
// outside Common.
func Name(code string) string {
	for _, l := range Common {
		if l.Code == code {
			return l.Name
		}
	}
	return code
}
