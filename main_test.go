package main

import (
	"net/http"
	"log"
	"io/ioutil"
	"fmt"
	"os"
	"testing"
	"github.com/torch/config"
	//"github.com/prometheus/prometheus/pkg/rulefmt"
)

func TestRules(t *testing.T) {

	cfg := &config.Config{}

	for _, r := range cfg.RuleFiles {
		fmt.Printf("%v", r)
	}

}

func TestValidator(t *testing.T) {

	//rgs, errs := rulefmt.ParseFile("1")
	//if errs != nil {
	//	return
	//}
	//
	//fmt.Printf("%v", rgs)
}

func TestRun(t *testing.T) {

	client := &http.Client{}

	response, err := client.Get("http://localhost:8080")
	if err != nil {
		log.Fatal(err)
		return
	}

	defer response.Body.Close()
	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Response: ", response.StatusCode)
	fmt.Println("Response: ", string(contents))

	err2 := ioutil.WriteFile("./response", contents, os.ModePerm)
	if err2 != nil {
		log.Fatal(err2)
	}
}
