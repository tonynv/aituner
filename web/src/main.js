import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';

if ('serviceWorker' in navigator) {
  addEventListener('load', () => navigator.serviceWorker.register('/sw.js').catch(() => {}));
}

export default mount(App, { target: document.getElementById('app') });
