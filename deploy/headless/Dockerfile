# Pin this to the occupied seat's image ID at build time. Retaining Chromium's
# exact version avoids a profile downgrade; only the running process tree changes.
ARG BROWSER_BASE
FROM ${BROWSER_BASE}
COPY cdp-proxy /usr/local/bin/cdp-proxy
COPY start.sh /usr/local/bin/headless-start
USER 1000:1000
ENV HOME=/config DISPLAY= \
    CHROME_PROFILE=/config/.config/chromium
HEALTHCHECK NONE
ENTRYPOINT ["/bin/bash", "/usr/local/bin/headless-start"]
CMD []
