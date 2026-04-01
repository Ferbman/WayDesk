package main

import (
"fmt"
"os"
)

func main() {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY|os.O_NONBLOCK, 0660)
	if err != nil {
		fmt.Println("No access to /dev/uinput:", err)
		os.Exit(1)
	}
	f.Close()
	fmt.Println("Success: Can open /dev/uinput")
}
