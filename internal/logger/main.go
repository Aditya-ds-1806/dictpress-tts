package logger

import (
	"log"
	"os"
)

var Logger = log.New(os.Stdout, "dictpress-tts: ", log.Ltime)
