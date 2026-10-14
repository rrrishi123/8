# build-runtime.sh supplies canonical cross-built artifacts and cockpit assets.
# This image is the runtime only: no Go, Node, model weights or second browser.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates curl jq tini sqlite git
COPY runtime/ /opt/four-system/
COPY entrypoint.sh /usr/local/bin/four-system-start
RUN chmod +x /usr/local/bin/four-system-start \
    && mkdir -p /root/.8
ENV PATH="/opt/four-system/8/collector:/opt/four-system/http-mcp/.bin:/opt/four-system/pilot/.bin:/opt/four-system/adapters/.bin:${PATH}" \
    EIGHT_ADAPTERS=/opt/four-system/adapters \
    EIGHT_WEB_DIR=/opt/four-system/8/web/dist \
    GOMEMLIMIT=128MiB
VOLUME ["/root/.8"]
EXPOSE 7070
HEALTHCHECK --interval=15s --timeout=3s --start-period=15s \
  CMD curl -fsS http://127.0.0.1:7070/health >/dev/null || exit 1
ENTRYPOINT ["/sbin/tini", "-g", "--", "/usr/local/bin/four-system-start"]
