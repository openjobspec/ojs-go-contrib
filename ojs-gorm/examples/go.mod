module github.com/openjobspec/ojs-go-contrib/ojs-gorm/examples

go 1.24.0

require (
	github.com/openjobspec/ojs-go-contrib/ojs-gorm v0.0.0
	github.com/openjobspec/ojs-go-sdk v0.5.0
	gorm.io/driver/postgres v1.5.0
	gorm.io/gorm v1.25.12
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/pgx/v5 v5.3.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	golang.org/x/crypto v0.6.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace github.com/openjobspec/ojs-go-contrib/ojs-gorm => ../
