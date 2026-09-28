(ns fixture.core
  (:require [ring.adapter.jetty :as jetty])
  (:gen-class))

(defn handler [_request]
  {:status 200
   :headers {"Content-Type" "text/html"}
   :body "<h1>https://clojure.build-value.test</h1>"})

(defn -main [& _args]
  (let [port (Integer/parseInt (or (System/getenv "PORT") "3000"))]
    (jetty/run-jetty handler {:port port :host "0.0.0.0"})))
