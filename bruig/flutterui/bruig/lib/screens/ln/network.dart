import 'dart:math' as math;

import 'package:bruig/models/snackbar.dart';
import 'package:collection/collection.dart';
import 'package:bruig/components/dcr_input.dart';
import 'package:bruig/components/info_grid.dart';
import 'package:bruig/components/inputs.dart';
import 'package:bruig/components/text.dart';
import 'package:bruig/screens/ln/components.dart';
import 'package:flutter/material.dart';
import 'package:golib_plugin/definitions.dart';
import 'package:golib_plugin/golib_plugin.dart';
import 'package:golib_plugin/util.dart';
import 'package:bruig/components/copyable.dart';
import 'package:tuple/tuple.dart';

class LNNetworkPage extends StatefulWidget {
  const LNNetworkPage({super.key});

  @override
  State<LNNetworkPage> createState() => _LNNetworkPageState();
}

class _PeerW extends StatelessWidget {
  final LNPeer peer;
  const _PeerW(this.peer);

  @override
  Widget build(BuildContext context) {
    return Column(children: [
      const SizedBox(height: 8),
      Row(children: [
        const SizedBox(width: 100, child: Txt.S("Peer ID:")),
        Expanded(child: Txt.S(peer.pubkey)),
      ]),
      const SizedBox(height: 8),
      Row(children: [
        const SizedBox(
            width: 100,
            child: Txt.S(
              "Address:",
            )),
        Txt.S(peer.address),
      ]),
      const SizedBox(height: 10),
      const Divider(),
    ]);
  }
}

class _LNNetworkPageState extends State<LNNetworkPage> {
  bool loading = true;
  bool connecting = false;
  bool querying = false;
  bool closed = true;
  bool graphLoading = false;
  LNGraphDescription graphDescription = LNGraphDescription.empty();
  String serverNode = "";
  List<LNPeer> peers = [];
  String lastQueriedNode = "";
  LNQueryRouteResponse queryRouteRes = LNQueryRouteResponse.empty();
  LNGetNodeInfoResponse nodeInfo = LNGetNodeInfoResponse.empty();
  final TextEditingController connectCtrl = TextEditingController();
  final TextEditingController queryRouteCtrl = TextEditingController();
  final AmountEditingController queryAmountCtrl = AmountEditingController();

  void closeNodeInfo() async {
    setState(() {
      queryRouteRes = LNQueryRouteResponse.empty();
      nodeInfo = LNGetNodeInfoResponse.empty();
    });
  }

  void loadInfo() async {
    var snackbar = SnackBarModel.of(context);
    setState(() {
      loading = true;
    });
    try {
      var newPeers = await Golib.lnListPeers();
      var newServerNode = await Golib.lnGetServerNode();
      setState(() {
        peers = newPeers;
        serverNode = newServerNode;
      });
    } catch (exception) {
      snackbar.error("Unable to load network info: $exception");
    } finally {
      setState(() {
        loading = false;
      });
    }
  }

  void connectToPeer() async {
    var snackbar = SnackBarModel.of(context);
    setState(() {
      connecting = true;
    });
    try {
      await Golib.lnConnectToPeer(connectCtrl.text);
      var newPeers = await Golib.lnListPeers();
      setState(() {
        connectCtrl.clear();
        peers = newPeers;
      });
    } catch (exception) {
      snackbar.error("Unable to connect to peer: $exception");
    } finally {
      setState(() {
        connecting = false;
      });
    }
  }

  Future<void> queryRouteToNode(String node, double amount) async {
    var snackbar = SnackBarModel.of(context);
    setState(() {
      querying = true;
    });
    try {
      if (amount < 0.00000001) {
        amount = 0.00000001;
      }

      var newNodeInfo = await Golib.lnGetNodeInfo(node);
      var newQueryRouteRes = await Golib.lnQueryRoute(amount, node);
      setState(() {
        nodeInfo = newNodeInfo;
        lastQueriedNode = node;
        queryRouteRes = newQueryRouteRes;
      });
    } catch (exception) {
      snackbar.error("Unable to query route: $exception");
    } finally {
      setState(() {
        querying = false;
        closed = false;
      });
    }
  }

  void queryRoute() async {
    await queryRouteToNode(queryRouteCtrl.text, queryAmountCtrl.amount);
  }

  void queryRouteToServer() async {
    await queryRouteToNode(serverNode, 0);
  }

  Future<void> loadGraph() async {
    var snackbar = SnackBarModel.of(context);
    setState(() {
      graphLoading = true;
    });
    try {
      var graph = await Golib.lnDescribeGraph();
      setState(() {
        graphDescription = graph;
      });
    } catch (exception) {
      snackbar.error("Unable to describe LN graph: $exception");
    } finally {
      setState(() {
        graphLoading = false;
      });
    }
  }

  LNGraphDescription limitedGraph({int maxNodes = 150}) {
    if (graphDescription.nodes.length <= maxNodes) {
      return graphDescription;
    }

    var selected = graphDescription.nodes.take(maxNodes).toList();
    var selectedKeys = selected.map((n) => n.pubkey).toSet();
    var edges = graphDescription.edges
        .where((e) =>
            selectedKeys.contains(e.node1Pub) &&
            selectedKeys.contains(e.node2Pub))
        .toList();
    return LNGraphDescription(selected, edges);
  }

  @override
  void initState() {
    super.initState();
    loadInfo();
    loadGraph();
  }

  Widget _buildChannel(LNChannelEdge chan) {
    var chanID = shortChanIDToStr(chan.channelID);
    var capacity = formatDCR(atomsToDCR(chan.capacity));

    return SimpleInfoGrid(colLabelSize: 130, [
      Tuple2(const Txt.S("Channel ID:"), Copyable.txt(Txt.S(chanID))),
      Tuple2(
          const Txt.S("Last Channel Update:"),
          Copyable.txt(Txt.S(
              DateTime.fromMillisecondsSinceEpoch(chan.lastUpdate * 1000)
                  .toIso8601String()))),
      Tuple2(const Txt.S("Channel Point:"),
          Copyable.txt(Txt.S(chan.channelPoint))),
      Tuple2(const Txt.S("Channel Capacity:"), Txt.S(capacity)),
      Tuple2(
          const Txt.S("Node 1:"),
          Copyable(chan.node1Pub,
              child: Txt.S(
                  "${chan.node1Policy.disabled ? '✗' : '✓'} ${chan.node1Pub}"))),
      Tuple2(
          const Txt.S("Node 2:"),
          Copyable(chan.node2Pub,
              child: Txt.S(
                  "${chan.node2Policy.disabled ? '✗' : '✓'} ${chan.node2Pub}"))),
    ]);
  }

  List<Widget> _buildNodeInfo(BuildContext context) {
    return [
      SimpleInfoGrid(colLabelSize: 130, [
        Tuple2(
            const Txt.S("PubKey"), Copyable.txt(Txt.S(nodeInfo.node.pubkey))),
        Tuple2(const Txt.S("Alias"), Copyable.txt(Txt.S(nodeInfo.node.alias))),
        Tuple2(const Txt.S("Number of Channels"),
            Txt.S(nodeInfo.numChannels.toString())),
        Tuple2(const Txt.S("Total Capacity"),
            Txt.S(formatDCR(atomsToDCR(nodeInfo.totalCapacity)))),
      ]),
      const SizedBox(height: 8),
      const LNInfoSectionHeader("Channels"),
      const SizedBox(height: 8),
      ...(nodeInfo.channels.isEmpty
          ? [const Text("No channels for node")]
          : nodeInfo.channels.map((chan) => _buildChannel(chan)).toList())
    ];
  }

  Widget _buildRouteHop(int hop, LNHop h) {
    var chanID = shortChanIDToStr(h.chanId);

    return Column(children: [
      SimpleInfoGrid(colLabelSize: 70, [
        Tuple2(const Txt.S("Hop:"), Txt.S(hop.toString())),
        Tuple2(const Txt.S("Node:"), Copyable.txt(Txt.S(h.pubkey))),
        Tuple2(const Txt.S("Channel ID:"), Copyable.txt(Txt.S(chanID))),
      ]),
      const SizedBox(height: 10),
    ]);
  }

  List<Widget> _buildRoute(BuildContext context, LNQueryRouteResponse res) {
    var successProb = (res.successProb * 100).toStringAsFixed(2);
    return [
      Txt.S("Success Probability: $successProb%"),
      const SizedBox(height: 8),
      const LNInfoSectionHeader("Route"),
      const SizedBox(height: 8),
      ...(res.routes.isEmpty
          ? [const Txt.S("No routes to node")]
          : res.routes[0].hops
              .mapIndexed((hop, h) => _buildRouteHop(hop, h))
              .toList()),
    ];
  }

  @override
  Widget build(BuildContext context) {
    if (loading) {
      return const Text("Loading...");
    }

    if (nodeInfo.node.pubkey != "" &&
        lastQueriedNode != "" &&
        queryRouteRes.routes.isNotEmpty) {
      return Container(
          padding: const EdgeInsets.all(16),
          child: Stack(alignment: Alignment.topRight, children: [
            SingleChildScrollView(
                child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                  const LNInfoSectionHeader("Node Info"),
                  const SizedBox(height: 8),
                  ..._buildNodeInfo(context),
                  const SizedBox(height: 12),
                  const LNInfoSectionHeader("Queried Route Results"),
                  const SizedBox(height: 8),
                  ..._buildRoute(context, queryRouteRes),
                ])),
            Positioned(
                top: 5,
                right: 5,
                child: IconButton(
                    tooltip: "Close",
                    iconSize: 15,
                    onPressed: () => closeNodeInfo(),
                    icon: const Icon(Icons.close_outlined)))
          ]));
    }

    var graph = limitedGraph();

    return Container(
        alignment: Alignment.topLeft,
        padding: const EdgeInsets.all(16),
        child: SingleChildScrollView(
          child:
              Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            const LNInfoSectionHeader("Lightning Graph Visualizer"),
            const SizedBox(height: 8),
            Row(children: [
              OutlinedButton(
                  onPressed: graphLoading ? null : loadGraph,
                  child: Txt.S(graphLoading ? "Loading graph..." : "Refresh Graph")),
              const SizedBox(width: 12),
              Txt.S("Nodes: ${graph.nodes.length}/${graphDescription.nodes.length}"),
              const SizedBox(width: 12),
              Txt.S("Channels: ${graph.edges.length}"),
            ]),
            const SizedBox(height: 8),
            Container(
                width: double.infinity,
                height: 360,
                decoration: BoxDecoration(
                    border: Border.all(color: Theme.of(context).colorScheme.onSurface.withOpacity(0.3))),
                child: graph.nodes.isEmpty
                    ? const Center(child: Txt.S("No graph data available"))
                    : Tooltip(
                        message: graphDescription.nodes.length > graph.nodes.length
                            ? "Showing first ${graph.nodes.length} nodes to keep rendering responsive"
                            : "Each point is a node and each line is a public channel",
                        child: LNGraphCanvas(graph)))),
            const SizedBox(height: 21),
            const LNInfoSectionHeader("Server Node"),
            const SizedBox(height: 8),
            Row(children: [
              const SizedBox(width: 100, child: Txt.S("Node ID:")),
              Expanded(child: Copyable.txt(Txt.S(serverNode))),
            ]),
            const SizedBox(height: 8),
            OutlinedButton(
                onPressed: !querying ? queryRouteToServer : null,
                child: const Txt.S("Query Route")),
            const SizedBox(height: 21),
            const LNInfoSectionHeader("Peers"),
            ...peers.map((peer) => _PeerW(peer)),
            const SizedBox(height: 8),
            Row(children: [
              const SizedBox(width: 110, child: Txt.S("Connect to Peer:")),
              Expanded(
                  child: TextInput(
                      textSize: TextSize.small,
                      controller: connectCtrl,
                      hintText: "Pubkey@ip:port"))
            ]),
            const SizedBox(height: 8),
            OutlinedButton(
                onPressed: !connecting ? connectToPeer : null,
                child: const Txt.S("Connect")),
            const SizedBox(height: 21),
            const LNInfoSectionHeader("Query Route"),
            Row(children: [
              const SizedBox(width: 110, child: Txt.S("Node ID:")),
              Expanded(
                  child: TextInput(
                      textSize: TextSize.small,
                      controller: queryRouteCtrl,
                      hintText: "Node ID")),
            ]),
            const SizedBox(height: 8),
            Row(children: [
              const SizedBox(width: 110, child: Txt.S("Amount:")),
              SizedBox(
                  width: 150,
                  child: dcrInput(
                      textSize: TextSize.small, controller: queryAmountCtrl))
            ]),
            OutlinedButton(
                onPressed: !querying ? queryRoute : null,
                child: const Text("Search")),
          ]),
        ));
  }
}


class LNGraphCanvas extends StatelessWidget {
  final LNGraphDescription graph;

  const LNGraphCanvas(this.graph, {super.key});

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(builder: (context, constraints) {
      var side = math.min(constraints.maxWidth, constraints.maxHeight);
      var center = Offset(constraints.maxWidth / 2, constraints.maxHeight / 2);
      var radius = side * 0.42;
      var positions = <String, Offset>{};
      for (var i = 0; i < graph.nodes.length; i++) {
        var angle = (2 * math.pi * i) / math.max(1, graph.nodes.length);
        positions[graph.nodes[i].pubkey] =
            center + Offset(math.cos(angle) * radius, math.sin(angle) * radius);
      }
      return CustomPaint(
          painter: _LNGraphPainter(graph, positions),
          size: Size(constraints.maxWidth, constraints.maxHeight));
    });
  }
}

class _LNGraphPainter extends CustomPainter {
  final LNGraphDescription graph;
  final Map<String, Offset> positions;

  _LNGraphPainter(this.graph, this.positions);

  @override
  void paint(Canvas canvas, Size size) {
    final edgePaint = Paint()
      ..color = Colors.blueGrey.withOpacity(0.35)
      ..strokeWidth = 1;
    final nodePaint = Paint()..color = Colors.lightBlueAccent;

    for (var edge in graph.edges) {
      var n1 = positions[edge.node1Pub];
      var n2 = positions[edge.node2Pub];
      if (n1 == null || n2 == null) {
        continue;
      }
      canvas.drawLine(n1, n2, edgePaint);
    }

    for (var node in graph.nodes) {
      var center = positions[node.pubkey];
      if (center == null) {
        continue;
      }
      canvas.drawCircle(center, 3, nodePaint);
    }
  }

  @override
  bool shouldRepaint(covariant _LNGraphPainter oldDelegate) {
    return oldDelegate.graph != graph;
  }
}
