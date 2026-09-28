(defproject fixture "0.1.0"
  :dependencies [[org.clojure/clojure "1.12.6"]
                 [ring/ring-jetty-adapter "1.15.3"]]
  :main ^:skip-aot fixture.core
  :profiles {:uberjar {:aot :all
                       :jvm-opts ["-Dclojure.compiler.direct-linking=true"]}})
