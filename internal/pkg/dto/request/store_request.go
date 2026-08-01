package request

type CreateStoreRequest struct {
	Name           string `json:"name"`
	BrandName      string `json:"brandName"`
	Address        string `json:"address"`
	NavigationName string `json:"navigationName"`
	Longitude      string `json:"longitude"`
	Latitude       string `json:"latitude"`
	MapProvider    string `json:"mapProvider"`
	ContactPhone   string `json:"contactPhone"`
	Remark         string `json:"remark"`
}

type UpdateStoreRequest struct {
	ID int64 `json:"id"`
	CreateStoreRequest
}

type UpdateStoreStatusRequest struct {
	ID     int64 `json:"id"`
	Status int   `json:"status"`
}
