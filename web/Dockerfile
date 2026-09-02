FROM node:24.19.0-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run check && npm run test && npm run build

FROM node:24.19.0-alpine AS runtime
ENV NODE_ENV=production HOST=0.0.0.0 PORT=3000
WORKDIR /app
RUN addgroup -S varyaone && adduser -S -G varyaone varyaone
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY --from=build --chown=varyaone:varyaone /app/build ./build
USER varyaone
EXPOSE 3000
CMD ["node", "build"]
