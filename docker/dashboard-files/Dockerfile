FROM node:20-alpine

WORKDIR /app

# Copy built dashboard files
COPY package.json package-lock.json* ./
COPY .next ./.next
COPY node_modules ./node_modules

ENV NODE_ENV=production
ENV NEXT_PUBLIC_API_URL=http://localhost:8080/api
ENV PORT=3000

EXPOSE 3000

CMD ["npx", "next", "start", "-p", "3000"]
