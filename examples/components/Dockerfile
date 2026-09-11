# SQLite 驱动需要 CGO；构建及运行镜像均为 Debian 12。
FROM golang:1.26.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -o /out/components-api ./examples/components/cmd/api
FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=build /out/components-api /app/components-api
ENV LOG_FILE_DISABLE=true
EXPOSE 8000 9001
ENTRYPOINT ["/app/components-api"]
CMD ["-config", "/app/configs/config.yaml"]
