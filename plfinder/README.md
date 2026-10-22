# PLfinder

## Compilation Instructions

### Prerequisites

* Go 1.22 or higher installed on your system.
* C/C++ compiler (such as GCC/MinGW on Windows) required for CGO dependencies (`webview_go`).

### Building the Executable

1. Open your terminal in the project root directory.

2. Download and tidy the dependencies:

   ```bash
   go mod tidy
   ```

3. Compile the application:

   * **Windows:**

     ```bash
     go build -ldflags="-H windowsgui" -o PLfinder.exe main.go
     ```

   * **Linux / macOS:**

     ```bash
     go build -o PLfinder main.go
     ```

The compiled executable (`PLfinder.exe` or `PLfinder`) will be generated in the root directory.

> **Note:** Ensure that `asnlookup.exe` and `reconhound.exe` are placed in the same folder as the compiled executable for the application to function correctly.