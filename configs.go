package Gogeo

import (
	"encoding/xml"
	"fmt"
	"os"
)

var MainConfig PGConfig

type PGConfig struct {
	XMLName  xml.Name `xml:"config"`
	Dbname   string   `xml:"dbname"`
	Host     string   `xml:"host"`
	Port     string   `xml:"port"`
	Username string   `xml:"user"`
	Password string   `xml:"password"`
}

func init() {

	xmlFile, err := os.Open("config.xml")
	if err != nil {
		fmt.Println("Error  opening  file:", err)
		return
	}
	defer xmlFile.Close()

	xmlDecoder := xml.NewDecoder(xmlFile)
	err = xmlDecoder.Decode(&MainConfig)
	if err != nil {
		fmt.Println("Error  decoding  XML:", err)
		return
	}

}
