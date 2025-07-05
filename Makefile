SRC = ./cmd/hail
TARGET = hail

default:
	go build -o $(TARGET) $(SRC)

clean:
	rm -f $(TARGET)

.PHONY: clean