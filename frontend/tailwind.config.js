// tailwind.config.js
/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./index.html", // <-- 确保这一行存在
    "./app.js"      // <-- 确保这一行存在
  ],
  theme: {
    extend: {},
  },
  plugins: [],
}

