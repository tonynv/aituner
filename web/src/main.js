import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import MenuBar from './MenuBar.svelte';

// ?view=menubar is the compact panel the macOS app shows under its menu bar icon.
const menubar = new URLSearchParams(location.search).get('view') === 'menubar';

if (!menubar && 'serviceWorker' in navigator) {
  addEventListener('load', () => navigator.serviceWorker.register('/sw.js').catch(() => {}));
}

export default mount(menubar ? MenuBar : App, { target: document.getElementById('app') });
