# 构建上下文是仓库根目录；消费项目复制后调整最后一个 go build 路径。
FROM golang:1.26.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/minimal-api ./examples/minimal/cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/minimal-api /app/minimal-api
COPY examples/minimal/configs/config.yaml /app/configs/config.yaml
ENV LOG_FILE_ENABLE=false
EXPOSE 8000 9001
ENTRYPOINT ["/app/minimal-api"]
CMD ["-config", "/app/configs/config.yaml"]
