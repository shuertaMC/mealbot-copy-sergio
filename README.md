# Intro (for people new to Go)
- Check out [A Tour of Go](https://tour.golang.org/welcome/1) to learn Go syntax
- Download Go [Downloads](https://golang.org/dl/)
- Walk through [How to Write Go Code](https://golang.org/doc/code.html) to learn how to fetch, build, and install Go code on your local machine

# Get Started
## Database
- Download Postgres (i.e the database that Mealbot uses) [here](https://www.postgresql.org/download/). See link for specific instructions for your OS.
- Create a Postgres database, and copy over the username and database name that you chose to 'db.go'
- Setup the database schema by executing 'schema.sql'
- NOTE: If the steps above for the database aren't super clear, please check out official Postgres documentation. For help on SQL syntax, check out [PostgreSQL Tutorial](http://www.postgresqltutorial.com/)

## Go 
- Build the project (`go build -o mealbot ./cmd/mealbot`)
- Run the executable ('./mealbot' or './mealbot pair')

# Miscellanea
- Package management is handled w/ Go Modules (https://blog.golang.org/using-go-modules)
