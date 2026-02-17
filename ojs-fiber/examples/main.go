package main

import (
	"log"

	"github.com/gofiber/fiber/v2"
	ojs "github.com/openjobspec/ojs-go-sdk"
	ojsfiber "github.com/openjobspec/ojs-go-contrib/ojs-fiber"
)

func main() {
	client, err := ojs.NewClient("http://localhost:8080")
	if err != nil {
		log.Fatal(err)
	}

	app := fiber.New()
	app.Use(ojsfiber.Middleware(client))

	app.Post("/send-email", func(c *fiber.Ctx) error {
		var req struct {
			To      string `json:"to"`
			Subject string `json:"subject"`
		}
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}

		err := ojsfiber.Enqueue(c, "email.send", ojs.Args{
			"to":      req.To,
			"subject": req.Subject,
		})
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}

		return c.JSON(fiber.Map{"status": "enqueued"})
	})

	log.Println("API server listening on :3000")
	log.Fatal(app.Listen(":3000"))
}
