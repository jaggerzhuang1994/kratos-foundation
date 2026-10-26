# 二进制由 run.sh 在宿主机交叉编译（-tags demo），镜像只负责打包。
FROM gcr.io/distroless/static-debian12:nonroot
COPY demo /app/demo
ENTRYPOINT ["/app/demo"]
