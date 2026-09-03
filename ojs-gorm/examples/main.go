package main

import (
	"log"
	"os"

	ojsgorm "github.com/openjobspec/ojs-go-contrib/ojs-gorm"
	ojs "github.com/openjobspec/ojs-go-sdk"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	Name  string
	Email string
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	if err := db.AutoMigrate(&User{}); err != nil {
		log.Fatal(err)
	}

	ojsURL := envOrDefault("OJS_URL", "http://localhost:8080")
	var clientOptions []ojs.ClientOption
	if token := os.Getenv("OJS_AUTH_TOKEN"); token != "" {
		clientOptions = append(clientOptions, ojs.WithAuthToken(token))
	}
	client, err := ojs.NewClient(ojsURL, clientOptions...)
	if err != nil {
		log.Fatal(err)
	}
	if err := ojsgorm.Register(db, client); err != nil {
		log.Fatal(err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		user := User{Name: "Alice", Email: "alice@example.com"}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		ojsgorm.EnqueueAfterCommit(tx, "welcome.email", ojs.Args{
			"name":  user.Name,
			"email": user.Email,
		})
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Println("User created and welcome email job enqueued")
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
