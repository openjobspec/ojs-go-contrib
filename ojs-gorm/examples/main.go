package main

import (
	"log"

	ojs "github.com/openjobspec/ojs-go-sdk"
	ojsgorm "github.com/openjobspec/ojs-go-contrib/ojs-gorm"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	Name  string
	Email string
}

func main() {
	dsn := "host=localhost user=ojs password=ojs dbname=ojs_example port=5432 sslmode=disable"
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}

	db.AutoMigrate(&User{})

	client, err := ojs.NewClient("http://localhost:8080")
	if err != nil {
		log.Fatal(err)
	}

	if err := ojsgorm.Register(db, client); err != nil {
		log.Fatal(err)
	}

	// Create a user and enqueue a welcome email atomically
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

	log.Println("User created and welcome email job enqueued!")
}
