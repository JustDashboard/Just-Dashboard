"use client"
export default function Home() {
  return <h1>{process.env.NEXT_PUBLIC_API_URL}</h1>
}
