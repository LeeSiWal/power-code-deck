/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        deck: {
          // 3-step near-black ground with a faint indigo bias.
          bg: '#0a0a0f',
          surface: '#101018',   // panels, inputs
          raised: '#17171f',    // menus, elevated cards
          border: '#26262f',    // hairline — lifted for definition
          'border-soft': '#1b1b24',
          accent: '#6366f1',
          'accent-light': '#818cf8',
          // Cool slate text, lifted so secondary/tertiary stay legible on near-black.
          text: '#e7e9f0',
          'text-dim': '#8791a4',
          'text-faint': '#565f73',
          success: '#22c55e',
          warning: '#f59e0b',
          danger: '#ef4444',
        },
      },
      animation: {
        'spin-slow': 'spin 8s linear infinite',
        'pulse-soft': 'pulse 3s ease-in-out infinite',
        'orbital': 'orbital 4s linear infinite',
        'fade-in': 'fadeIn 0.3s ease-out',
        'slide-up': 'slideUp 0.3s ease-out',
        'slide-down': 'slideDown 0.3s ease-out',
      },
      keyframes: {
        orbital: {
          '0%': { transform: 'rotate(0deg) translateX(30px) rotate(0deg)' },
          '100%': { transform: 'rotate(360deg) translateX(30px) rotate(-360deg)' },
        },
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
    },
  },
  plugins: [],
};
