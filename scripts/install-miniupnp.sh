#!/bin/sh

# SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
# SPDX-License-Identifier: MIT

MINIUPNPD_URL="http://miniupnp.free.fr/files/miniupnpd-2.3.3.tar.gz"
MINISSDPD_URL="http://miniupnp.free.fr/files/minissdpd-1.6.0.tar.gz"

# Install dependencies
apt -y install \
    libmnl-dev \
    libnfnetlink-dev \
    libnftnl-dev

# Fetch code
wget -O- ${MINIUPNPD_URL} | tar -xzv
wget -O- ${MINISSDPD_URL} | tar -xzv

# Install miniupnpd
pushd miniupnpd-2.3.3
./configure
popd

cat >> miniupnpd-2.3.3/config.h <<EOF
#define USE_MINIUPNPDCTL

#define ENABLE_LEASEFILE
#define LEASEFILE_USE_REMAINING_TIME
EOF

make -C miniupnpd-2.3.3 install

# Install minisspd
make -C minissdpd-1.6.0 install