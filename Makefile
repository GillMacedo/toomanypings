.PHONY: test lint demo repro

# examples/ is its own module (it needs grpc-go), and examples/broken is not
# Go on purpose, so gofmt only looks at the folders that must be formatted.
test:
	test -z "$$(gofmt -l plugin tests/integration_test.go examples/mismatch examples/healthy examples/repro)"
	go vet ./...
	go test -race ./...

lint:
	claude plugin validate ./plugin --strict
	claude plugin validate . --strict

# The mismatch example has findings on purpose, so exit 1 is expected. The
# healthy example must exit 0 and the broken one 3. The binary is built
# first because go run turns every failing exit code into 1.
demo:
	go build -o toomanypings ./plugin/scripts
	./toomanypings examples/mismatch || test $$? -eq 1
	./toomanypings examples/healthy
	./toomanypings examples/broken examples/healthy/server || test $$? -eq 3

# Runs real grpc-go clients and servers to show the GOAWAY. It downloads
# grpc-go, so it needs network access, Go 1.26 or newer, and about 5 minutes.
repro:
	cd examples && go run ./repro -client-time 10s -rpc 70s
	cd examples && go run ./repro -client-time 10s -server-min-time 10s -rpc 70s
	cd examples && go run ./repro -client-time 10s -server-time 2s -rpc 70s
	cd examples && go run ./repro -client-time 10s -client-pws -server-pws -rpc 0 -watch 70s
