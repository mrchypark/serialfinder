# Serial Finder
## Description

This package finds the USB serial devices connected to the computer, with the vendor id, product id, and serial number.

It can filter the devices by the vendor id and product id, or leave them empty to get all the devices.

## Supported Platforms
- Windows
- Linux
- MacOS

## Usage
```go
package main

import (
    "fmt"
    "github.com/hs0zip/serialfinder"
)

func main()
{
    devices, err := serialfinder.GetSerialDevices("1A86", "55D4")
    if err != nil {
        fmt.Println(err)
    }

    for _, device := range devices {
        fmt.Println(device)
    }
}
```

## License
MIT
