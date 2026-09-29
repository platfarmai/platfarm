module platfarm/__SVC_ID__

go 1.23

require (
	github.com/gin-gonic/gin v1.10.0
	github.com/platfarmai/sdk/go/pfauth v0.0.0
)

replace github.com/platfarmai/sdk/go/pfauth => ../../sdk/go/pfauth
