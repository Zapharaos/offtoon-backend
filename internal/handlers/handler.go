package handlers

type Handler struct {
	//trh  *toonrutime.Handler
}

// NewHandler creates a new handler wrapping both the set and search runtime handlers
func NewHandler( /*toonHandler *toonrutime.Handler*/ ) *Handler {
	return &Handler{
		//trh:  toonHandler,
	}
}
