package main

import (
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const JwtExpiration = 30 // seconds

func main() {
	if len(os.Args) != 2 {
		panic("wrong # of args")
	}
	key := []byte(os.Getenv("SECRET_KEY"))
	username := os.Args[1]

	t := jwt.NewWithClaims(jwt.SigningMethodHS256,
		jwt.MapClaims{
			"iss": "test-token-creator",
			"usn": username,
			"exp": time.Now().Unix() + JwtExpiration,
		})

	s, err := t.SignedString(key)
	if err != nil {
		panic(err)
	}
	fmt.Println(s)
}
