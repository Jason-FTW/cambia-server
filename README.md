# cambia-service

> Websocket-based game handler for Cambia

## Table of Contents

- [cambia-service](#cambia-service)
  - [Table of Contents](#table-of-contents)
  - [Getting Started](#getting-started)
    - [Prerequisites](#prerequisites)
    - [Installation](#installation)
    - [Running the Server](#running-the-server)
  - [License](#license)

## Getting Started

### Prerequisites

- **Go** version 1.20 or higher installed on your machine.

### Installation

1. **Clone the repository**

   ```bash
   git clone https://github.com/jason-s-yu/cambia.git
   cd cambia
   ```

2. **Install Dependencies**

   Ensure you have Go modules enabled:

   ```bash
   go mod tidy
   ```

   This will download the required dependencies specified in the `go.mod` file.

### Running the Server

Run the server using the `go run` command:

```bash
go run main.go
```

The server will start and listen on `http://localhost:8080`.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
