# Hint - Context-aware assistant for developers

`hint` is a CLI utility that helps developers get contextual suggestions based on the current working directory and its contents. The utility integrates with OpenAI-compatible APIs to generate relevant answers to your questions.

## Features

- Automatic analysis of the current directory to create context
- Support for various configuration methods (command line, environment variables, file)
- Compatibility with OpenAI and other compatible APIs (Azure OpenAI, etc.)
- Simple and intuitive interface

## Installation

### Using Homebrew (macOS/Linux)

The easiest way to install `hint` is using Homebrew:

```bash
brew install hint
```

### Building from source

If you prefer to build from source, you'll need:

- Go version 1.20 or higher
- Internet access for downloading dependencies
- API key for OpenAI or compatible service

Follow these steps:

1. Clone the repository:
   ```bash
   git clone https://github.com/mrYush/hint.git
   cd hint
   ```

2. Install dependencies:
   ```bash
   go mod tidy
   ```

3. Build:
   ```bash
   go build -o hint cmd/hint/main.go
   ```

4. Verify it works:
   ```bash
   ./hint --help
   ```

5. Install to your system:
   ```bash
   # Linux/MacOS
   sudo cp hint /usr/local/bin/
   # Windows
   cp hint.exe %USERPROFILE%\AppData\Local\bin\
   ```

## Configuration

The utility supports several configuration methods:

### Via command line flags

```bash
hint --api-key=<your_api_key> --model=<model>
```

### Via environment variables

```bash
export HINT_API_KEY=<your_api_key>
export HINT_MODEL=<model>
export HINT_API_URL=<api_url>
```

### Via configuration file

Create a file `~/.config/hint.yaml` or `./hint.yaml` with the following content:

```yaml
api_key: <your_api_key>
model: <model>
api_url: <api_url>
```

## Usage

### Basic examples

```bash
hint "How to use hint?"
```

### Common use cases

```bash
# Ask about how to run the project
hint "How do I run this project?"

# Ask about project structure
hint "Explain the directory structure of this project"

# Get help with a specific file
hint "What does the README.md file contain?"

# Ask for code explanation
hint "Explain what this code does"

# Get suggestions for solving issues
hint "I'm getting an error when running this command, what could be wrong?"
```

### Advanced usage

You can get more specific assistance by navigating to different directories in your project:

```bash
# Navigate to a subdirectory to focus the context
cd src/components/
hint "What do these components do?"
```

## How it works

1. When you run `hint` with a question, it captures your current working directory path
2. It scans the directory contents to create context
3. This context along with your question is sent to the configured LLM API
4. The LLM generates a response based on the context and question
5. The response is displayed in your terminal

## Troubleshooting

### Common issues

1. **Error "API key must be specified"**
   - Make sure you've set up your API key through one of the configuration methods
   - Check if the environment variable is properly set

2. **Error connecting to the API**
   - Verify your internet connection
   - Check if the API endpoint is correct and accessible

3. **Empty or irrelevant responses**
   - Try rephrasing your question to be more specific
   - Check if you're in the correct directory related to your question

### Debug mode

If you're having issues, you can get more detailed logs:

```bash
export HINT_DEBUG=true
hint "Your question"
```

## Contributing

Contributions are welcome! Here's how you can contribute:

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/your-feature-name`
3. Commit your changes: `git commit -am 'Add some feature'`
4. Push to the branch: `git push origin feature/your-feature-name`
5. Submit a pull request

## License

MIT
