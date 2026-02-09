FROM ubuntu:22.04

WORKDIR /workspace

COPY . /workspace

RUN chmod +x /workspace/benchmarks/run_benchmarks.sh

CMD ["/workspace/benchmarks/run_benchmarks.sh"]
