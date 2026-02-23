module github.com/openjobspec/ojs-go-contrib/ojs-gorm/examples

go 1.24

require (
	gorm.io/driver/postgres v1.5.0
	gorm.io/gorm v1.25.0
	github.com/openjobspec/ojs-go-contrib/ojs-gorm v0.0.0
	github.com/openjobspec/ojs-go-sdk v0.1.0
)

replace github.com/openjobspec/ojs-go-contrib/ojs-gorm => ../
