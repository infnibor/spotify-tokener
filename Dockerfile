# Use an official Bun runtime as a parent image
FROM oven/bun:latest

# Set the working directory in the container
WORKDIR /app

# Copy package.json and bun.lockb to the working directory
# This allows Bun to cache dependencies
COPY package.json ./

RUN bunx playwright install
# Install dependencies
RUN bun install --frozen-lockfile

# Copy the rest of the application code
COPY . .

# Expose the port the app runs on
EXPOSE 3000

# Command to run the application
CMD ["bun", "run", "src/app.ts"]
