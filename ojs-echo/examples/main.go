package main

import (
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
	ojs "github.com/openjobspec/ojs-go-sdk"
	ojsecho "github.com/openjobspec/ojs-go-contrib/ojs-echo"
)

func main() {
	client, err := ojs.NewClient("http://localhost:8080")
	if err != nil {
		log.Fatal(err)
	}

	e := echo.New()
	e.Use(ojsecho.Middleware(client))

	e.POST("/send-email", func(c echo.Context) error {
		var req struct {
			To      string `json:"to"`
			Subject string `json:"subject"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		err := ojsecho.Enqueue(c, "email.send", ojs.Args{
			"to":      req.To,
			"subject": req.Subject,
		})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"status": "enqueued"})
	})

	log.Println("API server listening on :3000")
	log.Fatal(e.Start(":3000"))
}
