package confluence

// Shapes for the Confluence Cloud REST API v1 subset cfmd uses.
// These mirror only the fields cfmd reads or writes; unused response fields
// are ignored during JSON decoding.

type Page struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Space     Space      `json:"space"`
	Version   Version    `json:"version"`
	Body      Body       `json:"body,omitempty"`
	Ancestors []Ancestor `json:"ancestors,omitempty"`
	Links     Links      `json:"_links,omitempty"`
}

type Space struct {
	Key string `json:"key"`
}

type Version struct {
	Number  int    `json:"number"`
	Message string `json:"message,omitempty"`
}

type Ancestor struct {
	ID string `json:"id"`
}

type Body struct {
	Storage StorageBody `json:"storage,omitempty"`
}

type StorageBody struct {
	Value          string `json:"value"`
	Representation string `json:"representation"`
}

type Links struct {
	Self   string `json:"self,omitempty"`
	WebUI  string `json:"webui,omitempty"`
	Base   string `json:"base,omitempty"`
	TinyUI string `json:"tinyui,omitempty"`
}

// PageCreate is the body for POST /rest/api/content.
type PageCreate struct {
	Type      string     `json:"type"`
	Title     string     `json:"title"`
	Space     Space      `json:"space"`
	Ancestors []Ancestor `json:"ancestors,omitempty"`
	Body      Body       `json:"body"`
}

// PageUpdate is the body for PUT /rest/api/content/{id}.
type PageUpdate struct {
	ID      string  `json:"id"`
	Type    string  `json:"type"`
	Title   string  `json:"title"`
	Space   Space   `json:"space"`
	Version Version `json:"version"`
	Body    Body    `json:"body"`
}

// CurrentUser is the response shape for /rest/api/user/current.
type CurrentUser struct {
	Type        string `json:"type"`
	Username    string `json:"username,omitempty"`
	UserKey     string `json:"userKey,omitempty"`
	AccountID   string `json:"accountId,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
}
