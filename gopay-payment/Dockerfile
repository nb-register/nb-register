ARG PYTHON_IMAGE=docker.m.daocloud.io/library/python:3.12-slim
FROM ${PYTHON_IMAGE}

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
ENV PIP_TRUSTED_HOST=pypi.tuna.tsinghua.edu.cn

WORKDIR /app

RUN if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
      sed -i \
        -e 's|https\?://deb.debian.org/debian|https://mirrors.tuna.tsinghua.edu.cn/debian|g' \
        -e 's|https\?://security.debian.org/debian-security|https://mirrors.tuna.tsinghua.edu.cn/debian-security|g' \
        /etc/apt/sources.list.d/debian.sources; \
    elif [ -f /etc/apt/sources.list ]; then \
      sed -i \
        -e 's|https\?://deb.debian.org/debian|https://mirrors.tuna.tsinghua.edu.cn/debian|g' \
        -e 's|https\?://security.debian.org/debian-security|https://mirrors.tuna.tsinghua.edu.cn/debian-security|g' \
        /etc/apt/sources.list; \
    fi \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY gopay-flow/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt

COPY gopay-flow/ /app/gopay-flow/
COPY NOTICE LICENSE run_gopay.sh run_payment_server.sh /app/

WORKDIR /app/gopay-flow
EXPOSE 50051 8081

ENTRYPOINT ["python", "payment_server.py"]
CMD ["--config", "config.json", "--listen", ":50051"]
