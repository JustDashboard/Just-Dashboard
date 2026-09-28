import { home } from "./routes"

document.querySelector("#api").textContent = import.meta.env.VITE_API_URL + " " + home.url()
