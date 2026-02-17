package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	ojs "github.com/openjobspec/ojs-go-sdk"
	ojsgin "github.com/openjobspec/ojs-go-contrib/ojs-gin"
)

func main() {
	client, err := ojs.NewClient("http://localhost:8080")
	if err != nil {
		log.Fatal(err)
	}

	r := gin.Default()
	r.Use(ojsgin.Middleware(client))

	r.POST("/send-email", func(c *gin.Context) {
		var req struct {
			To      string `json:"to"`
			Subject string `json:"subject"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		err := ojsgin.Enqueue(c, "email.send", ojs.Args{
			"to":      req.To,
			"subject": req.Subject,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "enqueued"})
	})

	log.Println("API server listening on :3000")
	log.Fatal(r.Run(":3000"))
}
